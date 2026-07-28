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

"""Rule for extracting health check schema from a go_library target."""

load("@rules_go//go:def.bzl", "GoSource")

def _health_schema_impl(ctx):
    go_info = ctx.attr.src[GoSource]
    all_srcs = go_info.srcs.to_list() if type(go_info.srcs) == "depset" else go_info.srcs
    srcs = [f for f in all_srcs if f.path.endswith(".go")]

    out = ctx.actions.declare_file(ctx.attr.name + ".json")

    ctx.actions.run(
        executable = ctx.executable._tool,
        arguments = ["-out=" + out.path] + [f.path for f in srcs],
        inputs = srcs,
        outputs = [out],
    )

    return [DefaultInfo(files = depset([out]))]

health_schema = rule(
    doc = """Extracts enum and struct metadata from a go_library target.

Uses the healthschema tool to parse Go AST and produce a JSON schema
with type descriptions derived from source comments.""",
    implementation = _health_schema_impl,
    attrs = {
        "src": attr.label(
            mandatory = True,
            providers = [GoSource],
            doc = "A go_library target to extract schema from.",
        ),
        "_tool": attr.label(
            default = "//catalog/cmd/healthschema",
            executable = True,
            cfg = "exec",
        ),
    },
)
