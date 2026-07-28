# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

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
        repository: Canonical registry repository for the image push target.
            When set (and oci = True), a `{name}.image.push` target is created
            that pushes the multi-arch index. CI overrides the destination at
            run time with --repository and supplies tags with --tag.
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
