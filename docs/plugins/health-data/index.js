/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

const path = require("path");
const fs = require("fs");

module.exports = function healthDataPlugin(context) {
  return {
    name: "health-data",
    async loadContent() {
      const staticDir = path.join(context.siteDir, "static", "data");
      const checksPath = path.join(staticDir, "checks.json");
      const schemaPath = path.join(staticDir, "schema.json");

      const checks = fs.existsSync(checksPath)
        ? JSON.parse(fs.readFileSync(checksPath, "utf-8"))
        : [];
      const schema = fs.existsSync(schemaPath)
        ? JSON.parse(fs.readFileSync(schemaPath, "utf-8"))
        : { enums: {}, structs: {} };

      return { checks, schema };
    },
    async contentLoaded({ content, actions }) {
      actions.setGlobalData(content);
    },
  };
};
