package claude

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

var unsupportedServerTools = map[string]bool{
	"web_search_20250305":    true,
	"web_search_20260318":    true,
	"web_fetch_20250910":     true,
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
