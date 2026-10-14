// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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
