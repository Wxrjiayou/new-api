package claude

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRemoveClaudeIterations_Present(t *testing.T) {
	input := `{"usage":{"input_tokens":8,"output_tokens":16,"iterations":[{"input_tokens":8,"output_tokens":16}],"service_tier":"standard"}}`
	result := removeClaudeIterations([]byte(input))

	require.False(t, gjson.GetBytes(result, "usage.iterations").Exists(), "iterations should be removed")
	assert.Equal(t, 8, int(gjson.GetBytes(result, "usage.input_tokens").Int()))
	assert.Equal(t, 16, int(gjson.GetBytes(result, "usage.output_tokens").Int()))
	assert.Equal(t, "standard", gjson.GetBytes(result, "usage.service_tier").String())
}

func TestRemoveClaudeIterations_Absent(t *testing.T) {
	input := `{"usage":{"input_tokens":8,"output_tokens":16,"service_tier":"standard"}}`
	result := removeClaudeIterations([]byte(input))
	assert.JSONEq(t, input, string(result))
}

func TestRemoveClaudeIterationsStr_MessageDelta(t *testing.T) {
	input := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":17,"output_tokens":27,"iterations":[{"input_tokens":17,"output_tokens":27,"type":"message"}]},"context_management":{"applied_edits":[]}}`
	result := removeClaudeIterationsStr(input)

	require.False(t, gjson.Get(result, "usage.iterations").Exists())
	assert.Equal(t, "message_delta", gjson.Get(result, "type").String())
	assert.Equal(t, "end_turn", gjson.Get(result, "delta.stop_reason").String())
	assert.Equal(t, 17, int(gjson.Get(result, "usage.input_tokens").Int()))
	assert.True(t, gjson.Get(result, "context_management").Exists())
}

func TestRemoveClaudeIterationsStr_NoIterations(t *testing.T) {
	input := `{"type":"message_delta","usage":{"input_tokens":22,"output_tokens":12}}`
	result := removeClaudeIterationsStr(input)
	assert.JSONEq(t, input, result)
}

func TestGenBase58RequestID(t *testing.T) {
	id := genBase58RequestID()
	require.True(t, strings.HasPrefix(id, "req_"), "should start with req_")
	assert.Len(t, id, 28, "req_ (4) + 24 chars = 28")

	for _, c := range id[4:] {
		assert.True(t, strings.ContainsRune(base58Alphabet, c), "char %c should be in base58 alphabet", c)
	}

	id2 := genBase58RequestID()
	assert.NotEqual(t, id, id2, "two IDs should differ")
}

func TestDeriveOrgID_Stable(t *testing.T) {
	id1 := deriveOrgID("sk-test-key-123")
	id2 := deriveOrgID("sk-test-key-123")
	assert.Equal(t, id1, id2, "same key should produce same org ID")

	id3 := deriveOrgID("sk-different-key")
	assert.NotEqual(t, id1, id3, "different keys should produce different org IDs")
}

func TestDeriveWorkspaceID_DiffersFromOrgID(t *testing.T) {
	orgID := deriveOrgID("sk-test-key-123")
	wsID := deriveWorkspaceID("sk-test-key-123")
	assert.NotEqual(t, orgID, wsID, "workspace ID should differ from org ID")

	ws1 := deriveWorkspaceID("sk-test-key-123")
	ws2 := deriveWorkspaceID("sk-test-key-123")
	assert.Equal(t, ws1, ws2, "same key should produce same workspace ID")
}

func TestRemoveClaudeIterations_NestedCacheCreation(t *testing.T) {
	input := `{"usage":{"input_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":16,"output_tokens_details":{"thinking_tokens":0},"service_tier":"standard","inference_geo":"global","iterations":[{"input_tokens":8,"output_tokens":16,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}]}}`
	result := removeClaudeIterations([]byte(input))

	require.False(t, gjson.GetBytes(result, "usage.iterations").Exists())
	assert.Equal(t, "standard", gjson.GetBytes(result, "usage.service_tier").String())
	assert.Equal(t, "global", gjson.GetBytes(result, "usage.inference_geo").String())
	assert.True(t, gjson.GetBytes(result, "usage.cache_creation").Exists())
	assert.True(t, gjson.GetBytes(result, "usage.output_tokens_details").Exists())
}
