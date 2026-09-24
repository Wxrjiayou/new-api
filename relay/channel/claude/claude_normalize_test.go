package claude

import (
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeClaudeBody_Full(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","id":"msg_abc","type":"message","role":"assistant","content":[{"type":"text","text":"Hi"}],"stop_reason":"end_turn","stop_sequence":null,"stop_details":null,"usage":{"input_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":16,"service_tier":"standard","inference_geo":"not_available","iterations":[{"input_tokens":8,"output_tokens":16}]},"input_transformations":[],"diagnostics":null,"context_management":{"applied_edits":[]}}`

	result := normalizeClaudeBody([]byte(input))

	require.False(t, gjson.GetBytes(result, "usage.iterations").Exists(), "iterations removed")
	require.False(t, gjson.GetBytes(result, "usage.cache_creation").Exists(), "usage.cache_creation removed")
	require.False(t, gjson.GetBytes(result, "context_management").Exists(), "context_management removed")
	require.False(t, gjson.GetBytes(result, "input_transformations").Exists(), "input_transformations removed")
	require.False(t, gjson.GetBytes(result, "diagnostics").Exists(), "diagnostics removed")
	assert.Equal(t, "global", gjson.GetBytes(result, "usage.inference_geo").String())
	assert.True(t, gjson.GetBytes(result, "container").Exists(), "container added")
	assert.Equal(t, gjson.Null, gjson.GetBytes(result, "container").Type, "container is null")
	assert.Equal(t, 8, int(gjson.GetBytes(result, "usage.input_tokens").Int()))
	assert.Equal(t, 0, int(gjson.GetBytes(result, "usage.cache_creation_input_tokens").Int()), "flat cache field preserved")
	assert.Equal(t, 16, int(gjson.GetBytes(result, "usage.output_tokens").Int()))
	assert.Equal(t, "standard", gjson.GetBytes(result, "usage.service_tier").String())
}

func TestNormalizeClaudeBody_AlreadyOfficial(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","id":"msg_abc","type":"message","container":null,"usage":{"input_tokens":8,"output_tokens":16,"service_tier":"standard","inference_geo":"global"}}`

	result := normalizeClaudeBody([]byte(input))

	assert.Equal(t, "global", gjson.GetBytes(result, "usage.inference_geo").String())
	assert.True(t, gjson.GetBytes(result, "container").Exists())
	assert.Equal(t, gjson.Null, gjson.GetBytes(result, "container").Type)
	require.False(t, gjson.GetBytes(result, "context_management").Exists())
}

func TestNormalizeClaudeBody_NoIterationsNoContextMgmt(t *testing.T) {
	input := `{"usage":{"input_tokens":8,"output_tokens":16,"service_tier":"standard","inference_geo":"not_available"}}`

	result := normalizeClaudeBody([]byte(input))

	assert.Equal(t, "global", gjson.GetBytes(result, "usage.inference_geo").String())
	assert.True(t, gjson.GetBytes(result, "container").Exists())
}

func TestNormalizeClaudeStreamEvent_MessageStart(t *testing.T) {
	input := `{"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_abc","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_details":null,"usage":{"input_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":1,"service_tier":"standard","inference_geo":"not_available"},"input_transformations":[],"diagnostics":null}}`

	result := normalizeClaudeStreamEvent(input, "message_start")

	assert.Equal(t, "global", gjson.Get(result, "message.usage.inference_geo").String())
	require.False(t, gjson.Get(result, "message.context_management").Exists())
	require.False(t, gjson.Get(result, "message.usage.cache_creation").Exists(), "cache_creation removed")
	require.False(t, gjson.Get(result, "message.input_transformations").Exists(), "input_transformations removed")
	require.False(t, gjson.Get(result, "message.diagnostics").Exists(), "diagnostics removed")
	assert.Equal(t, 0, int(gjson.Get(result, "message.usage.cache_creation_input_tokens").Int()), "flat cache field preserved")
	assert.True(t, gjson.Get(result, "message.container").Exists())
	assert.Equal(t, gjson.Null, gjson.Get(result, "message.container").Type)
	assert.Equal(t, "message_start", gjson.Get(result, "type").String())
}

func TestNormalizeClaudeStreamEvent_MessageStartWithContextMgmt(t *testing.T) {
	input := `{"type":"message_start","message":{"model":"claude-sonnet-4-6","context_management":{"applied_edits":[]},"usage":{"input_tokens":8,"output_tokens":1,"inference_geo":"not_available"}}}`

	result := normalizeClaudeStreamEvent(input, "message_start")

	require.False(t, gjson.Get(result, "message.context_management").Exists())
	assert.Equal(t, "global", gjson.Get(result, "message.usage.inference_geo").String())
	assert.True(t, gjson.Get(result, "message.container").Exists())
}

func TestNormalizeClaudeStreamEvent_MessageDelta(t *testing.T) {
	input := `{"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":16,"iterations":[{"input_tokens":8,"output_tokens":16}]},"context_management":{"applied_edits":[]}}`

	result := normalizeClaudeStreamEvent(input, "message_delta")

	require.False(t, gjson.Get(result, "context_management").Exists(), "context_management removed")
	require.False(t, gjson.Get(result, "usage.iterations").Exists(), "iterations removed")
	require.False(t, gjson.Get(result, "usage.cache_creation").Exists(), "cache_creation removed from delta")
	assert.True(t, gjson.Get(result, "delta.container").Exists(), "container added to delta")
	assert.Equal(t, gjson.Null, gjson.Get(result, "delta.container").Type)
	assert.Equal(t, "max_tokens", gjson.Get(result, "delta.stop_reason").String())
	assert.Equal(t, 16, int(gjson.Get(result, "usage.output_tokens").Int()))
	assert.Equal(t, 0, int(gjson.Get(result, "usage.cache_creation_input_tokens").Int()))
}

func TestNormalizeClaudeStreamEvent_MessageDeltaClean(t *testing.T) {
	input := `{"type":"message_delta","delta":{"stop_reason":"end_turn","container":null},"usage":{"input_tokens":8,"output_tokens":16}}`

	result := normalizeClaudeStreamEvent(input, "message_delta")

	assert.True(t, gjson.Get(result, "delta.container").Exists())
	assert.Equal(t, gjson.Null, gjson.Get(result, "delta.container").Type)
	assert.Equal(t, "end_turn", gjson.Get(result, "delta.stop_reason").String())
}

func TestNormalizeClaudeStreamEvent_ContentBlockDelta(t *testing.T) {
	input := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`

	result := normalizeClaudeStreamEvent(input, "content_block_delta")

	assert.JSONEq(t, input, result, "content_block_delta should not be modified")
}

func TestNormalizeClaudeStreamEvent_Ping(t *testing.T) {
	input := `{"type":"ping"}`
	result := normalizeClaudeStreamEvent(input, "ping")
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

func makeInfoWithToggles(force, body, headers bool) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelSetting: dto.ChannelSettings{
			ForceClaudeFormat:      force,
			NormalizeClaudeBody:    body,
			NormalizeClaudeHeaders: headers,
		},
	}
	return info
}

func TestGateFunctions(t *testing.T) {
	tests := []struct {
		name       string
		force      bool
		body       bool
		headers    bool
		wantBody   bool
		wantHeader bool
		wantAny    bool
	}{
		{"all off", false, false, false, false, false, false},
		{"body only", false, true, false, true, false, true},
		{"headers only", false, false, true, false, true, true},
		{"body+headers", false, true, true, true, true, true},
		{"force only", true, false, false, true, true, true},
		{"force+both", true, true, true, true, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := makeInfoWithToggles(tc.force, tc.body, tc.headers)
			assert.Equal(t, tc.wantBody, shouldNormalizeBody(info))
			assert.Equal(t, tc.wantHeader, shouldNormalizeHeaders(info))
			assert.Equal(t, tc.wantAny, shouldApplyClaudeNormalize(info))
		})
	}
}

func TestGateFunctions_NilInfo(t *testing.T) {
	assert.False(t, shouldNormalizeBody(nil))
	assert.False(t, shouldNormalizeHeaders(nil))
	assert.False(t, shouldApplyClaudeNormalize(nil))
}

func TestGateFunctions_NilChannelMeta(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	assert.False(t, shouldNormalizeBody(info))
	assert.False(t, shouldNormalizeHeaders(info))
	assert.False(t, shouldApplyClaudeNormalize(info))
}
