package claude

import (
	"fmt"
	"net/http"

	"github.com/tidwall/gjson"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// web_fetch stays rejected: AWS docs list neither web tool on Bedrock and
// LYQ (verified real Bedrock via signature platform bits) returns 400 for
// both. XDM accepting them was a conversion-layer artifact, not Bedrock
// behavior (1118 cross-check).
var unsupportedServerTools = map[string]bool{
	"web_search_20250305":     true,
	"web_search_20260318":     true,
	"web_fetch_20250910":      true,
	"web_fetch_20260209":      true,
	"code_execution_20250825": true,
}

// rejectInvalidThinkingBudget mimics Bedrock validation: enabled thinking
// requires budget_tokens strictly below max_tokens. MAX upstream accepts the
// illegal relation, so the gateway enforces it in Bedrock format.
func rejectInvalidThinkingBudget(request *dto.ClaudeRequest) *types.NewAPIError {
	if request.Thinking == nil || request.Thinking.Type != "enabled" ||
		request.Thinking.BudgetTokens == nil || request.MaxTokens == nil {
		return nil
	}
	if *request.Thinking.BudgetTokens >= int(*request.MaxTokens) {
		return types.WithClaudeError(types.ClaudeError{
			Type:    "invalid_request_error",
			Message: "thinking: budget_tokens must be less than max_tokens",
		}, http.StatusBadRequest)
	}
	return nil
}

// rejectBedrockIncompatibleParams mimics the strict Bedrock Runtime request
// schema (pydantic extra=forbid): fields that exist in our DTO but are
// silently filtered before forwarding, the unsupported priority service tier,
// the server-side fallbacks field (no DTO home — parsed away, so detected on
// the raw body), and the stream + max_tokens=0 conflict must be rejected with
// the upstream validation text verbatim instead of passing through as if
// accepted.
func rejectBedrockIncompatibleParams(request *dto.ClaudeRequest, rawBody []byte) *types.NewAPIError {
	if request.InferenceGeo != "" {
		return bedrockParamRejected("inference_geo: Extra inputs are not permitted")
	}
	if len(request.Speed) > 0 {
		return bedrockParamRejected("speed: Extra inputs are not permitted")
	}
	if request.ServiceTier == "priority" {
		return bedrockParamRejected("The provided service tier is not supported for this model.")
	}
	if gjson.GetBytes(rawBody, "fallbacks").Exists() {
		return bedrockParamRejected("fallbacks: Extra inputs are not permitted")
	}
	if request.MaxTokens != nil && *request.MaxTokens == 0 &&
		request.Stream != nil && *request.Stream {
		return bedrockParamRejected("stream cannot be true when max_tokens is 0")
	}
	return nil
}

func bedrockParamRejected(msg string) *types.NewAPIError {
	return types.WithClaudeError(types.ClaudeError{
		Type:    "invalid_request_error",
		Message: msg,
	}, http.StatusBadRequest)
}

// bedrockRequestOperation names the Invoke call the request shape maps to;
// the Bedrock SDK error shell quotes the same operation in both places.
func bedrockRequestOperation(request *dto.ClaudeRequest) string {
	if request.Stream != nil && *request.Stream {
		return "InvokeModelWithResponseStream"
	}
	return "InvokeModel"
}

// rejectUnsupportedServerTools checks the request tools for server-side tool
// types that Bedrock does not support. If any are found, it returns a 400
// error mimicking the Bedrock rejection message.
func rejectUnsupportedServerTools(request *dto.ClaudeRequest) *types.NewAPIError {
	tools := request.GetTools()
	if len(tools) == 0 {
		return nil
	}
	for _, tool := range tools {
		toolType := extractToolType(tool)
		if toolType != "" && unsupportedServerTools[toolType] {
			return types.WithClaudeError(types.ClaudeError{
				Type:    "invalid_request_error",
				Message: fmt.Sprintf("tool type '%s' is not supported for this model", toolType),
			}, http.StatusBadRequest)
		}
	}
	return nil
}

func extractToolType(tool any) string {
	switch t := tool.(type) {
	case *dto.ClaudeWebSearchTool:
		return t.Type
	case dto.ClaudeWebSearchTool:
		return t.Type
	case map[string]any:
		if typ, ok := t["type"].(string); ok {
			return typ
		}
	}
	return ""
}

var bedrockImageMediaTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// rejectUnsupportedImageMediaTypes mimics Bedrock's image validation: MAX
// accepts media types (e.g. image/bmp) that Bedrock rejects with this message.
func rejectUnsupportedImageMediaTypes(request *dto.ClaudeRequest) *types.NewAPIError {
	for i := range request.Messages {
		blocks, ok := request.Messages[i].Content.([]any)
		if !ok {
			continue
		}
		for _, block := range blocks {
			m, ok := block.(map[string]any)
			if !ok || m["type"] != "image" {
				continue
			}
			source, ok := m["source"].(map[string]any)
			if !ok {
				continue
			}
			mediaType, _ := source["media_type"].(string)
			if !bedrockImageMediaTypes[mediaType] {
				return types.WithClaudeError(types.ClaudeError{
					Type:    "invalid_request_error",
					Message: "Image media_type must be JPEG, PNG, GIF, or WebP",
				}, http.StatusBadRequest)
			}
		}
	}
	return nil
}

func restoreBedrockIDs(request *dto.ClaudeRequest) {
	for i := range request.Messages {
		blocks, ok := request.Messages[i].Content.([]any)
		if !ok {
			continue
		}
		for _, block := range blocks {
			m, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if tid, ok := m["tool_use_id"].(string); ok {
				m["tool_use_id"] = rewriteIDFromBedrock(tid, bedrockToolPrefix, "toolu_01")
			}
			if id, ok := m["id"].(string); ok {
				m["id"] = rewriteIDFromBedrock(id, bedrockToolPrefix, "toolu_01")
			}
		}
	}
}
