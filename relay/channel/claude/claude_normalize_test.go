package claude

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeClaudeBody_Full(t *testing.T) {
	input := `{"model":"claude-sonnet-4-6","id":"msg_abc","type":"message","role":"assistant","content":[{"type":"text","text":"Hi"}],"stop_reason":"end_turn","stop_sequence":null,"stop_details":null,"usage":{"input_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":16,"service_tier":"standard","inference_geo":"not_available","iterations":[{"input_tokens":8,"output_tokens":16}]},"input_transformations":[],"diagnostics":null,"context_management":{"applied_edits":[]}}`

	result := normalizeClaudeBody([]byte(input), false)

	require.False(t, gjson.GetBytes(result, "usage.iterations").Exists(), "iterations removed")
	require.False(t, gjson.GetBytes(result, "usage.cache_creation").Exists(), "usage.cache_creation removed")
	require.False(t, gjson.GetBytes(result, "context_management").Exists(), "context_management removed")
	require.False(t, gjson.GetBytes(result, "input_transformations").Exists(), "input_transformations removed")
	require.False(t, gjson.GetBytes(result, "diagnostics").Exists(), "diagnostics removed")
	require.False(t, gjson.GetBytes(result, "stop_details").Exists(), "stop_details removed")
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

	result := normalizeClaudeBody([]byte(input), false)

	assert.Equal(t, "global", gjson.GetBytes(result, "usage.inference_geo").String())
	assert.True(t, gjson.GetBytes(result, "container").Exists())
	assert.Equal(t, gjson.Null, gjson.GetBytes(result, "container").Type)
	require.False(t, gjson.GetBytes(result, "context_management").Exists())
}

func TestNormalizeClaudeBody_NoIterationsNoContextMgmt(t *testing.T) {
	input := `{"usage":{"input_tokens":8,"output_tokens":16,"service_tier":"standard","inference_geo":"not_available"}}`

	result := normalizeClaudeBody([]byte(input), false)

	assert.Equal(t, "global", gjson.GetBytes(result, "usage.inference_geo").String())
	assert.True(t, gjson.GetBytes(result, "container").Exists())
}

func TestNormalizeClaudeStreamEvent_MessageStart(t *testing.T) {
	input := `{"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_abc","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_details":null,"usage":{"input_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":1,"service_tier":"standard","inference_geo":"not_available"},"input_transformations":[],"diagnostics":null}}`

	result := normalizeClaudeStreamEvent(input, "message_start", false)

	assert.Equal(t, "global", gjson.Get(result, "message.usage.inference_geo").String())
	require.False(t, gjson.Get(result, "message.context_management").Exists())
	require.False(t, gjson.Get(result, "message.usage.cache_creation").Exists(), "cache_creation removed")
	require.False(t, gjson.Get(result, "message.input_transformations").Exists(), "input_transformations removed")
	require.False(t, gjson.Get(result, "message.diagnostics").Exists(), "diagnostics removed")
	require.False(t, gjson.Get(result, "message.stop_details").Exists(), "message.stop_details removed")
	assert.Equal(t, 0, int(gjson.Get(result, "message.usage.cache_creation_input_tokens").Int()), "flat cache field preserved")
	assert.True(t, gjson.Get(result, "message.container").Exists())
	assert.Equal(t, gjson.Null, gjson.Get(result, "message.container").Type)
	assert.Equal(t, "message_start", gjson.Get(result, "type").String())
}

func TestNormalizeClaudeStreamEvent_MessageStartWithContextMgmt(t *testing.T) {
	input := `{"type":"message_start","message":{"model":"claude-sonnet-4-6","context_management":{"applied_edits":[]},"usage":{"input_tokens":8,"output_tokens":1,"inference_geo":"not_available"}}}`

	result := normalizeClaudeStreamEvent(input, "message_start", false)

	require.False(t, gjson.Get(result, "message.context_management").Exists())
	assert.Equal(t, "global", gjson.Get(result, "message.usage.inference_geo").String())
	assert.True(t, gjson.Get(result, "message.container").Exists())
}

func TestNormalizeClaudeStreamEvent_MessageDelta(t *testing.T) {
	input := `{"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":16,"iterations":[{"input_tokens":8,"output_tokens":16}]},"context_management":{"applied_edits":[]}}`

	result := normalizeClaudeStreamEvent(input, "message_delta", false)

	require.False(t, gjson.Get(result, "context_management").Exists(), "context_management removed")
	require.False(t, gjson.Get(result, "usage.iterations").Exists(), "iterations removed")
	require.False(t, gjson.Get(result, "usage.cache_creation").Exists(), "cache_creation removed from delta")
	require.False(t, gjson.Get(result, "delta.stop_details").Exists(), "delta.stop_details removed")
	assert.True(t, gjson.Get(result, "delta.container").Exists(), "container added to delta")
	assert.Equal(t, gjson.Null, gjson.Get(result, "delta.container").Type)
	assert.Equal(t, "max_tokens", gjson.Get(result, "delta.stop_reason").String())
	assert.Equal(t, 16, int(gjson.Get(result, "usage.output_tokens").Int()))
	assert.Equal(t, 0, int(gjson.Get(result, "usage.cache_creation_input_tokens").Int()))
}

func TestNormalizeClaudeStreamEvent_MessageDeltaClean(t *testing.T) {
	input := `{"type":"message_delta","delta":{"stop_reason":"end_turn","container":null},"usage":{"input_tokens":8,"output_tokens":16}}`

	result := normalizeClaudeStreamEvent(input, "message_delta", false)

	assert.True(t, gjson.Get(result, "delta.container").Exists())
	assert.Equal(t, gjson.Null, gjson.Get(result, "delta.container").Type)
	assert.Equal(t, "end_turn", gjson.Get(result, "delta.stop_reason").String())
}

func TestNormalizeClaudeStreamEvent_ContentBlockDelta(t *testing.T) {
	input := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`

	result := normalizeClaudeStreamEvent(input, "content_block_delta", false)

	assert.JSONEq(t, input, result, "content_block_delta should not be modified")
}

func TestNormalizeClaudeStreamEvent_Ping(t *testing.T) {
	input := `{"type":"ping"}`
	result := normalizeClaudeStreamEvent(input, "ping", false)
	assert.JSONEq(t, input, result)
}

func TestGenRequestID(t *testing.T) {
	id := genRequestID()
	require.True(t, strings.HasPrefix(id, "req_01"), "should start with req_01")
	assert.Len(t, id, 28, "req_01 (6) + 7 timestamp + 15 random = 28")

	for _, c := range id[4:] {
		assert.True(t, strings.ContainsRune(base62Chars, c), "char %c should be in base62 alphabet", c)
	}

	id2 := genRequestID()
	assert.NotEqual(t, id, id2, "two IDs should differ")
}

func TestDeriveOrgID_Stable(t *testing.T) {
	id1 := deriveOrgID("sk-test-key-123")
	id2 := deriveOrgID("sk-test-key-123")
	assert.Equal(t, id1, id2, "same key should produce same org ID")

	id3 := deriveOrgID("sk-different-key")
	assert.NotEqual(t, id1, id3, "different keys should produce different org IDs")
}

func TestDeriveOrgID_UUIDv4Format(t *testing.T) {
	id := deriveOrgID("sk-test-key-123")
	parts := strings.Split(id, "-")
	require.Len(t, parts, 5, "UUID has 5 parts")
	assert.Len(t, parts[0], 8)
	assert.Len(t, parts[1], 4)
	assert.Len(t, parts[2], 4)
	assert.Len(t, parts[3], 4)
	assert.Len(t, parts[4], 12)
	assert.True(t, strings.HasPrefix(parts[2], "4"), "version nibble should be 4, got %s", parts[2])
	firstVariantChar := parts[3][0]
	assert.True(t, firstVariantChar == '8' || firstVariantChar == '9' || firstVariantChar == 'a' || firstVariantChar == 'b',
		"variant nibble should be 8/9/a/b, got %c", firstVariantChar)
}

func TestDeriveWorkspaceID_Format(t *testing.T) {
	wsID := deriveWorkspaceID("sk-test-key-123")
	require.True(t, strings.HasPrefix(wsID, "wrkspc_01"), "should start with wrkspc_01")
	assert.Len(t, wsID, 31, "wrkspc_01 (9) + 22 chars = 31")

	for _, c := range wsID[9:] {
		assert.True(t, strings.ContainsRune(base62Chars, c), "char %c should be in base62 alphabet", c)
	}

	ws1 := deriveWorkspaceID("sk-test-key-123")
	ws2 := deriveWorkspaceID("sk-test-key-123")
	assert.Equal(t, ws1, ws2, "same key should produce same workspace ID")

	ws3 := deriveWorkspaceID("sk-different-key")
	assert.NotEqual(t, ws1, ws3, "different keys should produce different workspace IDs")
}

func TestDeriveWorkspaceID_DiffersFromOrgID(t *testing.T) {
	orgID := deriveOrgID("sk-test-key-123")
	wsID := deriveWorkspaceID("sk-test-key-123")
	assert.NotEqual(t, orgID, wsID, "workspace ID should differ from org ID")
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

func TestNormalizeClaudeStreamEvent_ContentBlockDeltaThinking(t *testing.T) {
	input := `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hello","estimated_tokens":null}}`

	result := normalizeClaudeStreamEvent(input, "content_block_delta", false)

	require.False(t, gjson.Get(result, "delta.estimated_tokens").Exists(), "estimated_tokens removed")
	assert.Equal(t, "thinking_delta", gjson.Get(result, "delta.type").String())
	assert.Equal(t, "hello", gjson.Get(result, "delta.thinking").String())
}

func TestNormalizeClaudeStreamEvent_SignatureDeltaPreserved(t *testing.T) {
	// Signatures pass through unchanged: stripping broke multi-turn replay
	// against MAX ("Invalid signature in thinking block").
	maxSig := "EowCCqgBCBIYAipAc3duPkzTaEMW0m7JPMbEOEl6zlsI41lphmevbb3tiLZ1C6CywRun1StpGA8ZJIhKFY0Lg9h8qcZdLa6dczh+QzIPY2xhdWRlLW9wdXMtNC02OABCCHRoaW5raW5nWiQ1MDgyMDQ1ZS1kM2ExLTQwYjctOWVkNi05ZDc3ZTQ1MzZlNGZyECoIT/gREtyJulZW+Otv/w+IAQGoAdjn5tUGsAECEgxPNYRGn4nCPdt85VkaDCO0ySHwRXcgRNUrryIwSaUw8dVd+sZg2Cm/QY3NX0rNklBnJ2DytxNqmWh9gKVmOnqIuxIh7hFoiJtmUTr5KhGNz2GMRRrGah8EIe8GxWchexgB"
	input := `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"` + maxSig + `"}}`

	result := normalizeClaudeStreamEvent(input, "content_block_delta", false)

	assert.Equal(t, maxSig, gjson.Get(result, "delta.signature").String())
}

func TestNormalizeClaudeBody_ThinkingSignaturePreserved(t *testing.T) {
	maxSig := "EowCCqgBCBIYAipAc3duPkzTaEMW0m7JPMbEOEl6zlsI41lphmevbb3tiLZ1C6CywRun1StpGA8ZJIhKFY0Lg9h8qcZdLa6dczh+QzIPY2xhdWRlLW9wdXMtNC02OABCCHRoaW5raW5nWiQ1MDgyMDQ1ZS1kM2ExLTQwYjctOWVkNi05ZDc3ZTQ1MzZlNGZyECoIT/gREtyJulZW+Otv/w+IAQGoAdjn5tUGsAECEgxPNYRGn4nCPdt85VkaDCO0ySHwRXcgRNUrryIwSaUw8dVd+sZg2Cm/QY3NX0rNklBnJ2DytxNqmWh9gKVmOnqIuxIh7hFoiJtmUTr5KhGNz2GMRRrGah8EIe8GxWchexgB"
	input := `{"model":"claude-opus-4-6","content":[{"type":"thinking","thinking":"5","signature":"` + maxSig + `"},{"type":"text","text":"hello"}],"usage":{"input_tokens":8,"output_tokens":16}}`

	result := normalizeClaudeBody([]byte(input), false)

	assert.Equal(t, maxSig, gjson.GetBytes(result, "content.0.signature").String())
	assert.Equal(t, "text", gjson.GetBytes(result, "content.1.type").String(), "text block preserved")
}

func TestNormalizeClaudeBody_SafeguardResultsRemoved(t *testing.T) {
	input := `{"content":[],"safeguard_results":[],"usage":{"input_tokens":8,"output_tokens":16}}`
	result := normalizeClaudeBody([]byte(input), false)
	assert.False(t, gjson.GetBytes(result, "safeguard_results").Exists())

	start := `{"type":"message_start","message":{"safeguard_results":[],"usage":{"input_tokens":8}}}`
	assert.False(t, gjson.Get(normalizeClaudeStreamEvent(start, "message_start", false), "message.safeguard_results").Exists())

	delta := `{"type":"message_delta","delta":{"stop_reason":"end_turn","safeguard_results":[]},"usage":{"output_tokens":16}}`
	assert.False(t, gjson.Get(normalizeClaudeStreamEvent(delta, "message_delta", false), "delta.safeguard_results").Exists())
}

func TestBedrockOverlaySpeed(t *testing.T) {
	// MAX returns speed:"standard"; real Bedrock usage has no speed key at
	// all in either mode (1117/1118 dual-reference captures).
	body := `{"id":"msg_bdrk_01x","usage":{"input_tokens":8,"output_tokens":16,"speed":"standard"}}`
	result := bedrockOverlayBody([]byte(body))
	assert.False(t, gjson.GetBytes(result, "usage.speed").Exists())

	start := `{"type":"message_start","message":{"id":"msg_bdrk_01x","usage":{"input_tokens":8,"speed":"standard"}}}`
	assert.False(t, gjson.Get(bedrockOverlayStreamEvent(start, "message_start"), "message.usage.speed").Exists())

	delta := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":16,"speed":"standard"}}`
	assert.False(t, gjson.Get(bedrockOverlayStreamEvent(delta, "message_delta"), "usage.speed").Exists())
}

func TestBedrockOverlayTopLevelKeyOrder(t *testing.T) {
	body := `{"model":"claude-sonnet-4-6","id":"msg_01ABC","type":"message","role":"assistant",` +
		`"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","stop_sequence":null,` +
		`"usage":{"input_tokens":8,"output_tokens":10}}`
	result := bedrockOverlayBody([]byte(body))
	var keys []string
	gjson.GetBytes(result, "@this").ForEach(func(k, _ gjson.Result) bool {
		keys = append(keys, k.String())
		return true
	})
	assert.Equal(t, []string{
		"model", "id", "type", "role", "content",
		"stop_reason", "stop_sequence", "stop_details", "usage",
	}, keys, "stop_details reordered before usage; container stays deleted for the Bedrock format")
	assert.True(t, strings.HasPrefix(gjson.GetBytes(result, "id").String(), "msg_bdrk_"))
}

func TestBedrockOverlayMessageIDNewFormat(t *testing.T) {
	body := `{"id":"msg_01ABC","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":8,"output_tokens":10}}`
	result := bedrockOverlayBody([]byte(body))
	id := gjson.GetBytes(result, "id").String()
	assert.Regexp(t, `^msg_bdrk_[a-z0-9]{52}$`, id, "XJB-class new-generation message id")

	// Idempotent: an id already in the new format survives unchanged.
	again := bedrockOverlayBody(result)
	assert.Equal(t, id, gjson.GetBytes(again, "id").String())

	start := `{"type":"message_start","message":{"id":"msg_01ABC","usage":{"input_tokens":8}}}`
	got := bedrockOverlayStreamEvent(start, "message_start")
	assert.Regexp(t, `^msg_bdrk_[a-z0-9]{52}$`, gjson.Get(got, "message.id").String())

	// Old-format Bedrock ids from a Bedrock-upstream pool regenerate too;
	// tool ids keep the prefix-swap behaviour.
	old := `{"id":"msg_bdrk_01XyZ","content":[{"type":"tool_use","id":"toolu_01ABC","name":"f","input":{}}]}`
	swapped := bedrockOverlayBody([]byte(old))
	assert.Regexp(t, `^msg_bdrk_[a-z0-9]{52}$`, gjson.GetBytes(swapped, "id").String())
	assert.Equal(t, "toolu_bdrk_01ABC", gjson.GetBytes(swapped, "content.0.id").String())
}

func TestRejectBedrockIncompatibleParams(t *testing.T) {
	zero := uint(0)
	stream := true
	notStream := false
	for _, tc := range []struct {
		name string
		req  *dto.ClaudeRequest
		want string
	}{
		{"inference_geo", &dto.ClaudeRequest{InferenceGeo: "us"}, "inference_geo: Extra inputs are not permitted"},
		{"speed", &dto.ClaudeRequest{Speed: json.RawMessage(`"fast"`)}, "speed: Extra inputs are not permitted"},
		{"service_tier", &dto.ClaudeRequest{ServiceTier: "priority"}, "The provided service tier is not supported for this model."},
		{"zero stream", &dto.ClaudeRequest{MaxTokens: &zero, Stream: &stream}, "stream cannot be true when max_tokens is 0"},
	} {
		apiErr := rejectBedrockIncompatibleParams(tc.req)
		require.NotNil(t, apiErr, tc.name)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode, tc.name)
		assert.Equal(t, tc.want, apiErr.Error(), tc.name)
	}
	// non-stream zero keeps the current one-token behaviour; clean passes.
	assert.Nil(t, rejectBedrockIncompatibleParams(&dto.ClaudeRequest{MaxTokens: &zero, Stream: &notStream}))
	assert.Nil(t, rejectBedrockIncompatibleParams(&dto.ClaudeRequest{ServiceTier: "standard"}))
	assert.Nil(t, rejectBedrockIncompatibleParams(&dto.ClaudeRequest{}))
}

func TestWrapBedrockRequestErrorOperationName(t *testing.T) {
	inner := types.WithClaudeError(types.ClaudeError{
		Type: "invalid_request_error", Message: "stream cannot be true when max_tokens is 0",
	}, http.StatusBadRequest)
	streamed := wrapBedrockRequestError(inner, "InvokeModelWithResponseStream")
	assert.Contains(t, streamed.Error(),
		"operation error Bedrock Runtime: InvokeModelWithResponseStream, https response error StatusCode: 400")
	plain := wrapBedrockRequestError(inner, "InvokeModel")
	assert.Contains(t, plain.Error(),
		"operation error Bedrock Runtime: InvokeModel, https response error StatusCode: 400")
	streamOn := true
	assert.Equal(t, "InvokeModelWithResponseStream", bedrockRequestOperation(&dto.ClaudeRequest{Stream: &streamOn}))
	assert.Equal(t, "InvokeModel", bedrockRequestOperation(&dto.ClaudeRequest{}))
}

func TestWrapBedrockRequestErrorNoDoubleWrap(t *testing.T) {
	original := types.WithClaudeError(types.ClaudeError{Type: "invalid_request_error", Message: "temperature: range: 0..1"}, http.StatusBadRequest)
	wrapped := WrapBedrockRequestError(original)
	require.Contains(t, wrapped.Error(), "operation error Bedrock Runtime")
	require.Equal(t, 1, strings.Count(wrapped.Error(), "operation error Bedrock Runtime"))

	reWrapped := WrapBedrockRequestError(wrapped)
	assert.Equal(t, wrapped.Error(), reWrapped.Error(), "already-wrapped error must not be wrapped again")
}

func TestBedrockRequestValidation(t *testing.T) {
	imageRequest := func(mediaType string) *dto.ClaudeRequest {
		return &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{Role: "user", Content: []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mediaType, "data": "AQID"}},
		}}}}
	}
	for _, mt := range []string{"image/jpeg", "image/png", "image/gif", "image/webp"} {
		assert.Nil(t, rejectUnsupportedImageMediaTypes(imageRequest(mt)), mt)
	}
	apiErr := rejectUnsupportedImageMediaTypes(imageRequest("image/bmp"))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, "Image media_type must be JPEG, PNG, GIF, or WebP", apiErr.Error())

	toolRequest := func(toolType string) *dto.ClaudeRequest {
		return &dto.ClaudeRequest{Tools: []any{map[string]any{"type": toolType, "name": "t"}}}
	}
	assert.NotNil(t, rejectUnsupportedServerTools(toolRequest("web_search_20250305")))
	assert.NotNil(t, rejectUnsupportedServerTools(toolRequest("code_execution_20250825")))
	assert.NotNil(t, rejectUnsupportedServerTools(toolRequest("web_fetch_20250910")), "docs + LYQ reject web_fetch; XDM acceptance was a conversion-layer artifact")
	assert.NotNil(t, rejectUnsupportedServerTools(toolRequest("web_fetch_20260209")))
}

func TestRejectInvalidThinkingBudget(t *testing.T) {
	budget := 4096
	lowBudget := 1024
	max2048 := uint(2048)

	rejected := &dto.ClaudeRequest{
		Thinking:  &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		MaxTokens: &max2048,
	}
	apiErr := rejectInvalidThinkingBudget(rejected)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, "thinking: budget_tokens must be less than max_tokens", apiErr.Error())

	accepted := []*dto.ClaudeRequest{
		{Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &lowBudget}, MaxTokens: &max2048},
		{Thinking: &dto.Thinking{Type: "adaptive", BudgetTokens: &budget}, MaxTokens: &max2048},
		{Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &budget}},
		{MaxTokens: &max2048},
	}
	for _, req := range accepted {
		assert.Nil(t, rejectInvalidThinkingBudget(req))
	}
}

func TestBedrockOverlayUsageKeyRemoval(t *testing.T) {
	// Real Bedrock usage omits inference_geo / speed / iterations /
	// server_tool_use entirely, and carries output_tokens_details only when
	// thinking actually occurred.
	body := `{"id":"msg_bdrk_01x","usage":{"input_tokens":8,"output_tokens":16,` +
		`"inference_geo":"global","iterations":null,"server_tool_use":{"web_search_requests":0},` +
		`"speed":"standard","output_tokens_details":null,"service_tier":"standard"}}`
	result := bedrockOverlayBody([]byte(body))
	for _, key := range []string{"usage.inference_geo", "usage.iterations", "usage.server_tool_use", "usage.speed", "usage.output_tokens_details"} {
		assert.False(t, gjson.GetBytes(result, key).Exists(), key)
	}
	assert.Equal(t, "standard", gjson.GetBytes(result, "usage.service_tier").String(), "service_tier preserved")

	withThinking := `{"id":"msg_bdrk_01y","usage":{"input_tokens":8,"output_tokens":16,"output_tokens_details":{"thinking_tokens":42}}}`
	result = bedrockOverlayBody([]byte(withThinking))
	assert.EqualValues(t, 42, gjson.GetBytes(result, "usage.output_tokens_details.thinking_tokens").Int(), "real breakdown preserved")

	start := `{"type":"message_start","message":{"id":"msg_bdrk_01x","usage":{"input_tokens":8,` +
		`"inference_geo":"global","iterations":null,"server_tool_use":null,"output_tokens_details":null,"speed":"fast"}}}`
	streamed := bedrockOverlayStreamEvent(start, "message_start")
	for _, key := range []string{"message.usage.inference_geo", "message.usage.iterations", "message.usage.server_tool_use", "message.usage.output_tokens_details", "message.usage.speed"} {
		assert.False(t, gjson.Get(streamed, key).Exists(), key)
	}
	assert.True(t, gjson.Get(streamed, "message.usage.cache_creation").Exists(), "cache_creation nesting preserved")
}

func TestNormalizeContextManagementEchoForBedrock(t *testing.T) {
	// LYQ (verified real Bedrock) echoes context_management when the beta is
	// applied; the Bedrock path must pass it through instead of stripping.
	body := `{"content":[],"context_management":{"applied_edits":[{"type":"clear_tool_uses"}]},"usage":{"input_tokens":8,"output_tokens":16}}`
	result := normalizeClaudeBody([]byte(body), true)
	assert.True(t, gjson.GetBytes(result, "context_management.applied_edits").Exists())
	official := normalizeClaudeBody([]byte(body), false)
	assert.False(t, gjson.GetBytes(official, "context_management").Exists(), "still stripped in official format")

	start := `{"type":"message_start","message":{"model":"claude-sonnet-4-6","context_management":{"applied_edits":[{"type":"clear_thinking"}]},"usage":{"input_tokens":8,"output_tokens":1}}}`
	assert.True(t, gjson.Get(normalizeClaudeStreamEvent(start, "message_start", true), "message.context_management").Exists())
	delta := `{"type":"message_delta","context_management":{"applied_edits":[{"type":"clear_tool_uses"}]},"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":16}}`
	assert.True(t, gjson.Get(normalizeClaudeStreamEvent(delta, "message_delta", true), "context_management").Exists())

	// Empty envelopes (MAX emits them on every request, Bedrock references
	// do not) are stripped; only applied edits pass through.
	emptyBody := `{"content":[],"context_management":{"applied_edits":[]},"usage":{"input_tokens":8,"output_tokens":16}}`
	assert.False(t, gjson.GetBytes(normalizeClaudeBody([]byte(emptyBody), true), "context_management").Exists())
	emptyStart := `{"type":"message_start","message":{"context_management":{"applied_edits":[]},"usage":{"input_tokens":8}}}`
	assert.False(t, gjson.Get(normalizeClaudeStreamEvent(emptyStart, "message_start", true), "message.context_management").Exists())
	assert.True(t, gjson.Get(normalizeClaudeStreamEvent(`{"type":"message_start","message":{"context_management":{"applied_edits":[{"type":"clear_tool_uses"}]},"usage":{}}}`, "message_start", true), "message.context_management").Exists())
}
