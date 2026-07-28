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

PLATFORMS = {
    "linux_amd64": {
        "os": "linux",
        "cpu": "x86_64",
        "exec_properties": {
            "OSFamily": "Linux",
            "Arch": "amd64",
        },
    },
    "linux_arm64": {
        "os": "linux",
        "cpu": "arm64",
        "exec_properties": {
            "container-image": "docker://ghcr.io/catthehacker/ubuntu:act-22.04@sha256:5f9c35c25db1d51a8ddaae5c0ba8d3c163c5e9a4a6cc97acd409ac7eae239448",
            "OSFamily": "linux",
        },
    },
    "darwin_amd64": {
        "os": "osx",
        "cpu": "x86_64",
        "exec_properties": {},
    },
    "darwin_arm64": {
        "os": "osx",
        "cpu": "arm64",
        "exec_properties": {},
    },
}

def register_platforms():
    native.platform(
        name = "default_host_platform",
        exec_properties = {"no-remote-exec": "true"},
        parents = ["@platforms//host"],
    )
    for name, p in PLATFORMS.items():
        native.platform(
            name = name,
            constraint_values = [
                "@platforms//os:" + p["os"],
                "@platforms//cpu:" + p["cpu"],
            ],
            exec_properties = p["exec_properties"],
            visibility = ["//visibility:public"],
        )
