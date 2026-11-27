// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A panic in a tool call, in the SDK's own code included, becomes a tool error.
func TestAPanicInAToolCallIsAToolError(t *testing.T) {
	handler := receive(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		panic("boom: " + hostile)
	})

	result, err := handler(t.Context(), "tools/call", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "hv_list_checks"}})
	require.NoError(t, err)

	call, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)
	assert.True(t, call.IsError)
	require.Len(t, call.Content, 1)
	assert.Equal(t, "error executing the tool", call.Content[0].(*mcp.TextContent).Text)
}

func TestNullArgumentsBecomeAnEmptyObject(t *testing.T) {
	var seen json.RawMessage

	handler := receive(func(_ context.Context, _ string, request mcp.Request) (mcp.Result, error) {
		seen = request.(*mcp.CallToolRequest).Params.Arguments
		return &mcp.CallToolResult{}, nil
	})

	for _, arguments := range []string{"null", " null\n"} {
		_, err := handler(t.Context(), "tools/call",
			&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(arguments)}})
		require.NoError(t, err)
		assert.JSONEq(t, "{}", string(seen))
	}
}

func TestLineErrors(t *testing.T) {
	for line, want := range map[string][]byte{
		"not json":  parseError,
		`{"a":`:     parseError,
		"123":       invalidRequest,
		`"text"`:    invalidRequest,
		`{"foo":1}`: invalidRequest,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`:             nil,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`:               nil,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`: nil,
	} {
		assert.Equal(t, want, lineError([]byte(line)), line)
	}
}
