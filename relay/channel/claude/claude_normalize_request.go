package claude

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// web_fetch is intentionally absent: real Bedrock accepts the tool declaration
// (without executing it), so rejecting it would diverge from the reference.
var unsupportedServerTools = map[string]bool{
	"web_search_20250305":     true,
	"web_search_20260318":     true,
	"code_execution_20250825": true,
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
