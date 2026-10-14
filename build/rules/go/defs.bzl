# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

load("@rules_go//go:def.bzl", go_binary_wrapper = "go_binary")
load("//build/rules/oci:defs.bzl", "DEFAULT_BASE_IMAGE", "go_image")

def go_binary(
        name,
        oci = False,
        base_image = DEFAULT_BASE_IMAGE,
        tars = [],
        repository = None,
        **kwargs):
    """go_binary wrapper that optionally creates OCI image targets.

    Args:
        name: The name of the go_binary target.
        oci: If True, also create OCI image targets.
        base_image: The base image to use for the OCI image.
        tars: Additional layers to add to the image.
        repository: Optional default registry repository for the push target.
            CI supplies the actual destination at run time with --repository.
        **kwargs: Additional arguments passed to go_binary.
    """
    go_binary_wrapper(
        name = name,
        **kwargs
    )

    if oci:
        go_image(
            name = name,
            srcs = name,
            base_image = base_image,
            tars = tars,
            repository = repository,
            tags = ["no-cache"],
        )
