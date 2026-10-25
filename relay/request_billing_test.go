package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareRequestBillingRejectsSensitiveWordsWith400(t *testing.T) {
	previousEnabled, previousPrompt, previousWords := setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled, setting.SensitiveWords
	setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = true, true
	setting.SensitiveWordsFromString("forbidden")
	t.Cleanup(func() {
		setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled, setting.SensitiveWords = previousEnabled, previousPrompt, previousWords
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{Request: &dto.ClaudeRequest{
		Model:    "claude-test",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "say the forbidden word"}},
	}}

	apiErr := PrepareRequestBilling(c, info)

	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeSensitiveWordsDetected, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Contains(t, apiErr.Error(), "forbidden")
	assert.Equal(t, "invalid_request_error", apiErr.ToClaudeError().Type)
	assert.Nil(t, info.Billing, "a rejected request must not pre-consume quota")
}
