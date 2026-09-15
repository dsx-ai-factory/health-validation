"""Extracts the Go module list the linker embedded in a binary.

The emitted list is the linker's own record of the third-party modules in the
shipped artifact, which is what container scanners read. It is not a complete
inventory of the image: in-repo modules are omitted because rules_go emits no
module metadata for them, so the linked catalog never appears; the base layer is
out of scope,
a dependency whose BUILD files use the legacy go_default_library naming drops
out silently (see the cel-go note in third_party/go.MODULE.bazel), and only the
linux/amd64 half of the published multi-arch index is captured.
"""

load("@rules_go//go:def.bzl", "GoArchive", "go_context")

# The shipped artifact is linux/amd64. A darwin build of the same target
# succeeds but silently drops github.com/NVIDIA/go-nvml, so without this
# transition the test would pass on a macOS workstation against the wrong list.
# The arm64 half of the index emits an identical module set today, so one
# recorded list covers both published architectures.
def _linux_amd64_transition(_settings, _attr):
    return {"//command_line_option:platforms": "//build/platforms:linux_amd64"}

linux_amd64_transition = transition(
    implementation = _linux_amd64_transition,
    inputs = [],
    outputs = ["//command_line_option:platforms"],
)

def _go_shipped_modules_impl(ctx):
    go = go_context(ctx)
    binary = ctx.files.binary[0]

    # The "_generated" suffix keeps this off the checked-in golden's name.
    # macOS filesystems are case-insensitive, so a bare shipped_modules.txt
    # collides with SHIPPED_MODULES.txt in the diff_test runfiles tree and the
    # test fails to build on every Mac while passing on Linux CI.
    out = ctx.actions.declare_file(ctx.label.name + "_generated.txt")

    # pipefail reports the failure at `go version -m` itself. Without it a
    # failing invocation still exits 0, and the only symptom is a header-only
    # output file that surfaces later as a confusing wholesale golden diff.
    #
    # The ELF check is what actually protects the linux transition. On a linux
    # host removing `cfg = linux_amd64_transition` changes nothing, so no test
    # can see it; off linux it silently drops github.com/NVIDIA/go-nvml. Failing
    # on a non-ELF input turns that into a clear error at the point of the bug.
    #
    # GOROOT and the SDK root file come from go.env / go.sdk rather than being
    # hand-rolled: cmd/go otherwise locates GOROOT by resolving its own path out
    # of the execroot, which works only while Bazel stages bin/go as a symlink.
    ctx.actions.run_shell(
        inputs = [binary],
        outputs = [out],
        tools = [go.sdk.go, go.sdk.root_file],
        toolchain = "@rules_go//go:toolchain",
        command = """set -euo pipefail
case "$(head -c 4 "$2" | od -An -tx1 | tr -d ' \n')" in
  7f454c46) ;;
  *) echo "$2 is not an ELF binary; the linux transition was removed or overridden" >&2; exit 1 ;;
esac
cat > "$3" <<'HEADER'
# Third-party Go modules embedded in the shipped linux/amd64 health-validation
# binary. Generated; edit by running
# //toolkit/cmd/health-validation:shipped_modules_update.
#
# This is a Go-binary inventory, not a container inventory. It omits first-party
# modules resolved through a `replace` (catalog is linked but never listed), says
# nothing about the image's base layer, can undercount a dependency whose BUILD
# files use the legacy go_default_library naming, and records only the
# linux/amd64 half of the published multi-arch index.
HEADER
"$1" version -m "$2" | awk -F'\t' '$2 == "dep" { print $3 "\t" $4 }' | LC_ALL=C sort >> "$3"
""",
        arguments = [go.sdk.go.path, binary.path, out.path],
        env = dict(go.env, GOTOOLCHAIN = "local"),
        mnemonic = "GoShippedModules",
        progress_message = "Extracting embedded module list from %{label}",
    )
    return [DefaultInfo(files = depset([out]))]

go_shipped_modules = rule(
    implementation = _go_shipped_modules_impl,
    doc = "Emits the module list the Go linker embedded in `binary`, one tab-separated module and version per line.",
    attrs = {
        "binary": attr.label(
            allow_single_file = True,
            cfg = linux_amd64_transition,
            doc = "The linked go_binary whose embedded module list to extract.",
        ),
    },
    toolchains = ["@rules_go//go:toolchain"],
)

def _go_linked_modules_impl(ctx):
    # GoArchive.transitive is the binary's whole Go closure, already in the
    # target configuration, so no graph query and no nogo subtraction is needed.
    # It carries package import paths, not module paths, so the action folds
    # each package up to the longest module in `recorded` that prefixes it.
    # A package no module covers is emitted verbatim and shows up as an extra
    # line in the diff, which is how a linked-but-unrecorded module surfaces.
    #
    # Folding alone is not enough: a module nested under a recorded one would be
    # absorbed by its parent's prefix and never surface. Each go_deps repository
    # is exactly one module, so the action also rejects two repositories folding
    # to the same recorded module.
    archives = ctx.attr.binary[0][GoArchive].transitive.to_list()
    paths = ctx.actions.declare_file(ctx.label.name + "_importpaths.txt")
    ctx.actions.write(paths, "".join(sorted([(a.label.workspace_name or "_main") + "\t" + a.importpath + "\n" for a in archives if a.importpath])))

    out = ctx.actions.declare_file(ctx.label.name + "_generated.txt")
    ctx.actions.run_shell(
        inputs = [paths, ctx.file.recorded],
        outputs = [out],
        command = """set -euo pipefail
awk -v first_party="$4" '
  NR == FNR { mods[++n] = $0; next }
  $1 == first_party { next }
  {
    best = ""
    for (i = 1; i <= n; i++)
      if (index($2, mods[i]) == 1 &&
          (length($2) == length(mods[i]) || substr($2, length(mods[i]) + 1, 1) == "/") &&
          length(mods[i]) > length(best)) best = mods[i]
    if (best == "") { print $2; next }
    if (best in owner && owner[best] != $1)
      print "ABSORBED " best " <- " owner[best] " and " $1
    owner[best] = $1
    seen[best] = 1
  }
  END { for (m in seen) print m }
' "$2" "$1" | LC_ALL=C sort -u > "$3"
""",
        arguments = [paths.path, ctx.file.recorded.path, out.path, ctx.attr.first_party_repo],
        mnemonic = "GoLinkedModules",
        progress_message = "Deriving linked module set for %{label}",
    )
    return [DefaultInfo(files = depset([out]))]

go_linked_modules = rule(
    implementation = _go_linked_modules_impl,
    doc = "Emits the module set reachable from `binary`'s Go closure, folded from package import paths.",
    attrs = {
        "binary": attr.label(
            cfg = linux_amd64_transition,
            doc = "The go_binary whose transitive Go closure to walk.",
        ),
        "recorded": attr.label(
            allow_single_file = True,
            doc = "One module path per line, supplying the vocabulary to fold against.",
        ),
        "first_party_repo": attr.string(
            default = "_main",
            doc = "Bazel repository name holding in-repo modules, which carry no module metadata.",
        ),
    },
)
