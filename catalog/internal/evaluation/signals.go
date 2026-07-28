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

package evaluation

import (
	"reflect"
	"strings"
)

// ToSignals converts a typed measurement struct into the map[string]any
// that CEL evaluation expects. Keys are derived from json struct tags.
func ToSignals(m any) map[string]any {
	v := reflect.ValueOf(m)
	t := v.Type()

	out := make(map[string]any, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key, _, _ := strings.Cut(tag, ",")
		out[key] = v.Field(i).Interface()
	}
	return out
}
