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

package health

import (
	"encoding/json"
	"fmt"
)

// marshalEnum encodes an int-backed enum as a JSON string using the
// provided name mapping. Used by all domain enum types in this package.
func marshalEnum[T ~int](v T, names map[T]string) ([]byte, error) {
	name, ok := names[v]
	if !ok {
		return nil, fmt.Errorf("unknown enum value %d", int(v))
	}
	return json.Marshal(name)
}

// unmarshalEnum decodes a JSON string into an int-backed enum using
// the provided reverse mapping. typeName is used in error messages.
func unmarshalEnum[T ~int](data []byte, fromName map[string]T, target *T, typeName string) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	v, ok := fromName[name]
	if !ok {
		return fmt.Errorf("unknown %s %q", typeName, name)
	}
	*target = v
	return nil
}
