package types

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToClaudeErrorUsesDocumentedTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *NewAPIError
		want string
	}{
		{name: "upstream claude type kept", err: WithClaudeError(ClaudeError{Type: "overloaded_error", Message: "busy"}, 529), want: "overloaded_error"},
		{name: "empty claude type mapped from 529", err: WithClaudeError(ClaudeError{Message: "busy"}, 529), want: "overloaded_error"},
		{name: "openai error without code", err: NewOpenAIError(errors.New("busy"), ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable), want: "overloaded_error"},
		{name: "openai error with non-claude code", err: WithOpenAIError(OpenAIError{Message: "slow down", Code: "rate_limit_exceeded"}, http.StatusTooManyRequests), want: "rate_limit_error"},
		{name: "local error 400", err: NewErrorWithStatusCode(errors.New("bad"), ErrorCodeInvalidRequest, http.StatusBadRequest), want: "invalid_request_error"},
		{name: "local error 401", err: NewErrorWithStatusCode(errors.New("no auth"), ErrorCodeInvalidRequest, http.StatusUnauthorized), want: "authentication_error"},
		{name: "local error 413", err: NewErrorWithStatusCode(errors.New("large"), ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge), want: "request_too_large"},
		{name: "local error 500", err: NewError(errors.New("boom"), ErrorCodeBadResponse), want: "api_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.err.ToClaudeError().Type)
		})
	}
}
