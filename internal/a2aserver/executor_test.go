package a2aserver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStructuredResultUnwrapsMCPJSONObject(t *testing.T) {
	result := structuredResult(`{"payload":{"evm_chain_id":"2517","to":"0x1234"}}`)
	encoded, err := json.Marshal(Response{OK: true, Result: result})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(encoded, &got))
	_, isString := got["result"].(string)
	require.False(t, isString, "MCP JSON must remain an A2A object, not a quoted string")
	payload := got["result"].(map[string]any)["payload"].(map[string]any)
	require.Equal(t, "2517", payload["evm_chain_id"])
}

func TestStructuredResultLeavesPlainTextUntouched(t *testing.T) {
	require.Equal(t, "no assets configured", structuredResult("no assets configured"))
	require.Equal(t, "42", structuredResult("42"))
}
