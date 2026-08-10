load("@aspect_bazel_lib//lib:transitions.bzl", "platform_transition_filegroup")
load("@rules_oci//oci:defs.bzl", "oci_image", "oci_image_index", "oci_load", "oci_push")
load("@rules_pkg//pkg:tar.bzl", "pkg_tar")
load("//build/rules/oci:dpkg_status.bzl", "merged_dpkg_status")
load("//build/rules/oci:transition.bzl", "multi_arch")

# Must be a glibc userland (Ubuntu), NOT scratch / static-distroless. Images
# built from this macro package cgo binaries — health-validation, for one, dynamically
# links glibc (amd64: /lib64/ld-linux-x86-64.so.2; arm64: /lib/ld-linux-aarch64.so.1)
# and dlopen()s the NVIDIA driver's libnvidia-ml.so.1 at runtime (the driver is
# injected by the NVIDIA Container Toolkit, not baked in). A scratch/static base
# would leave the binary with no dynamic loader and the container would fail to
# start. Only drop to a minimal base after confirming the packaged binary is
# statically linked.
DEFAULT_BASE_IMAGE = "@ubuntu_noble"

def go_image(
        name,
        srcs,
        tars = [],
        base_image = DEFAULT_BASE_IMAGE,
        visibility = ["//visibility:public"],
        tags = [],
        repository = None):
    """Packages a go_binary into an OCI image.

    Args:
        name: The name of the image.
        srcs: The go_binary target to package.
        tars: Additional layers to add to the image.
        base_image: The base image to use. Defaults to ubuntu noble. Must be a
            glibc base for cgo/dynamically-linked binaries — see DEFAULT_BASE_IMAGE.
        visibility: The visibility of the image targets.
        tags: Additional tags to apply to the image.
        repository: Optional default registry repository for the push target. CI
            should supply the destination at run time with --repository.
    """
    app_layer = "{}.image.app_layer".format(name)
    pkg_tar(
        name = app_layer,
        srcs = [srcs],
    )
    tars = tars + [app_layer]

    _image(name, tars, base_image, visibility, tags, repository)

def _image(name, tars, base_image, visibility, tags = [], repository = None):
    # nvcr.io/nvidia/base/ubuntu arm64 omits /lib/ld-linux-aarch64.so.1 (the cgo PT_INTERP); select() resolves after the multi_arch transition.
    arm64_linker_layer = "{}.image.arm64_linker_symlink".format(name)
    pkg_tar(
        name = arm64_linker_layer,
        symlinks = select({
            "@platforms//cpu:arm64": {
                "lib/ld-linux-aarch64.so.1": "/usr/lib/ld-linux-aarch64.so.1",
            },
            "//conditions:default": {},
        }),
        tags = ["manual", "no-remote-cache"] + tags,
    )
    tars = tars + [arm64_linker_layer]

    # An apt layer's own /var/lib/dpkg/status replaces the base's, hiding every
    # base package from image scanners. Rebuild the database from both and stack
    # it last so it wins. See dpkg_status.bzl.
    dpkg_status_layer = "{}.image.dpkg_status".format(name)
    merged_dpkg_status(
        name = dpkg_status_layer,
        base = base_image,
        layers = tars,
        tags = ["manual", "no-remote-cache"] + tags,
    )
    tars = tars + [dpkg_status_layer]

    image = "{}.image.pre_transitioned_image".format(name)
    oci_image(
        name = image,
        base = base_image,
        tars = tars,
        entrypoint = ["/{}".format(name)],
        tags = ["manual", "no-remote-cache"] + tags,
        visibility = visibility,
    )

    # Platform transition so macOS hosts build a linux container for the current
    # target CPU (the host CPU under normal defaults, but a --platforms override
    # wins — e.g. --platforms=//build/platforms:linux_amd64 on an arm64 host
    # selects amd64). Transition to //build/platforms:linux_* (the repo's platforms with the
    # CC/cgo toolchain + RBE exec_properties wired in), NOT
    # @rules_go//go/toolchain:linux_*: the latter lacks this repo's C toolchain
    # config, so cgo silently disables and a cgo binary like health-validation fails to
    # link its NVML bindings. This must match the platforms the multi_arch
    # transition below uses, or local_load and the multi-arch index would build
    # the single-arch image two different ways.
    transitioned_image = "{}.image".format(name)
    platform_transition_filegroup(
        name = transitioned_image,
        srcs = [image],
        target_platform = select({
            "@platforms//cpu:arm64": "//build/platforms:linux_arm64",
            "@platforms//cpu:x86_64": "//build/platforms:linux_amd64",
        }),
        visibility = visibility,
        tags = ["manual", "no-remote-cache"] + tags,
    )

    # Load into local container runtime (`bazel run //...:NAME.image.local_load`,
    # then `docker run --rm NAME:latest ...`). Tag with the image name, not the
    # Bazel package path, so the local tag is e.g. health-validation:latest rather than
    # cmd/health-validation:latest.
    local_load = "{}.image.local_load".format(name)
    oci_load(
        name = local_load,
        image = transitioned_image,
        repo_tags = ["{}:latest".format(name)],
        tags = ["manual", "oci-load", "no-remote-cache"],
        visibility = visibility,
    )

    native.filegroup(
        name = "{}.image.tar".format(name),
        srcs = [local_load],
        output_group = "tarball",
        tags = ["oci-tarball", "no-remote-cache"],
        visibility = visibility,
    )

    # Multi-architecture image.
    multi_arch_image = "{}.image.multi_arch".format(name)
    multi_arch(
        name = multi_arch_image,
        image = image,
        platforms = [
            "//build/platforms:linux_arm64",
            "//build/platforms:linux_amd64",
        ],
        visibility = visibility,
        tags = ["manual", "no-remote-cache"] + tags,
    )

    image_index = "{}.image.index".format(name)
    oci_image_index(
        name = image_index,
        images = [multi_arch_image],
        visibility = visibility,
        tags = ["manual", "no-remote-cache"] + tags,
    )

    # CI supplies the repository and tags at runtime; the default is inert.
    oci_push(
        name = "{}.image.push".format(name),
        image = image_index,
        repository = repository or "example.invalid/{}".format(name),
        visibility = visibility,
        tags = ["manual", "no-remote-cache", "oci-push"] + tags,
    )
