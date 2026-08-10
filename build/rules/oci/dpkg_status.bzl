"""Reconcile a base image's dpkg database with the apt layers stacked on it.

rules_distroless' `dpkg_status` describes only the packages it installed, and
its layer replaces the base image's `/var/lib/dpkg/status` wholesale. Image
scanners then catalog the overlay's packages and none of the base's, which
reports a fraction of the image as the whole of it. This rule rebuilds the
database from the base plus every layer that carries one and emits it as a
further layer, so the image describes itself completely.
"""

_TAR_TOOLCHAIN = "@aspect_bazel_lib//lib:tar_toolchain_type"

def _merged_dpkg_status_impl(ctx):
    bsdtar = ctx.toolchains[_TAR_TOOLCHAIN]
    output = ctx.actions.declare_file(ctx.attr.name + ".tar")

    args = ctx.actions.args()
    args.add(bsdtar.tarinfo.binary)
    args.add(output)

    # .path, not the File: Args refuses directories, which may expand to many values.
    args.add(ctx.file.base.path)
    args.add_all(ctx.files.layers)

    ctx.actions.run(
        executable = ctx.executable._merge_sh,
        inputs = depset([ctx.file.base] + ctx.files.layers),
        outputs = [output],
        tools = [bsdtar.default.files],
        arguments = [args],
        mnemonic = "MergeDpkgStatus",
        progress_message = "Merging dpkg database for %{label}",
    )

    return [DefaultInfo(files = depset([output]))]

merged_dpkg_status = rule(
    doc = "Produces a tar layer holding the reconciled /var/lib/dpkg/status.",
    implementation = _merged_dpkg_status_impl,
    attrs = {
        "base": attr.label(
            doc = "OCI layout directory of the base image, as produced by oci.pull.",
            allow_single_file = True,
            mandatory = True,
        ),
        "layers": attr.label_list(
            doc = "Tars stacked on the base. Those without a dpkg database are ignored.",
            allow_files = True,
        ),
        "_merge_sh": attr.label(
            allow_single_file = True,
            executable = True,
            cfg = "exec",
            default = ":merge_dpkg_status.sh",
        ),
    },
    toolchains = [_TAR_TOOLCHAIN],
)
