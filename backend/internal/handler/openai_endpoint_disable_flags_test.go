package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIEndpointDisableFlags_BlockConfiguredEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	trueValue := true
	tests := []struct {
		name        string
		path        string
		body        string
		group       *service.Group
		run         func(*gin.Context)
		wantMessage string
	}{
		{
			name:        "openai_responses",
			path:        "/openai/v1/responses",
			body:        `{"model":"gpt-5","input":"hello"}`,
			group:       &service.Group{DisableResponsesAPI: &trueValue},
			run:         newOpenAIHandlerForPreviousResponseIDValidation(t, nil).Responses,
			wantMessage: "Responses API is disabled for this group",
		},
		{
			name:        "openai_chat_completions",
			path:        "/openai/v1/chat/completions",
			body:        `{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`,
			group:       &service.Group{DisableChatCompletionsAPI: &trueValue},
			run:         newOpenAIHandlerForPreviousResponseIDValidation(t, nil).ChatCompletions,
			wantMessage: "Chat Completions API is disabled for this group",
		},
		{
			name:        "gateway_responses",
			path:        "/v1/responses",
			body:        `{"model":"claude-sonnet-4-5","input":"hello"}`,
			group:       &service.Group{DisableResponsesAPI: &trueValue},
			run:         (&GatewayHandler{}).Responses,
			wantMessage: "Responses API is disabled for this group",
		},
		{
			name:        "gateway_chat_completions",
			path:        "/v1/chat/completions",
			body:        `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`,
			group:       &service.Group{DisableChatCompletionsAPI: &trueValue},
			run:         (&GatewayHandler{}).ChatCompletions,
			wantMessage: "Chat Completions API is disabled for this group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newEndpointDisableFlagContext(http.MethodPost, tt.path, tt.body, tt.group)

			tt.run(c)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			require.Equal(t, tt.wantMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
		})
	}
}

func TestOpenAIEndpointDisableFlags_NilAndFalseDoNotBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)

	falseValue := false
	tests := []struct {
		name  string
		group *service.Group
	}{
		{name: "nil_flags", group: &service.Group{}},
		{
			name: "false_flags",
			group: &service.Group{
				DisableResponsesAPI:       &falseValue,
				DisableChatCompletionsAPI: &falseValue,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newEndpointDisableFlagContext(
				http.MethodPost,
				"/v1/responses",
				`{"model":"claude-sonnet-4-5","stream":"true","input":"hello"}`,
				tt.group,
			)

			(&GatewayHandler{}).Responses(c)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			require.Equal(t, invalidStreamFieldTypeMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
		})
	}
}

func TestOpenAIEndpointDisableFlags_BlockResponsesWebSocketBeforeUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)

	trueValue := true
	group := &service.Group{DisableResponsesAPI: &trueValue}
	c, rec := newEndpointDisableFlagContext(http.MethodGet, "/openai/v1/responses", "", group)
	c.Request.Header.Set("Upgrade", "websocket")
	c.Request.Header.Set("Connection", "Upgrade")

	newOpenAIHandlerForPreviousResponseIDValidation(t, nil).ResponsesWebSocket(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Responses API is disabled for this group", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
}

func TestOpenAIEndpointDisableFlags_BlockGatewayAliasRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	trueValue := true
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		group       *service.Group
		run         func(*gin.Context)
		wantMessage string
	}{
		{
			name:        "bare_responses_alias",
			method:      http.MethodPost,
			path:        "/responses",
			body:        `{"model":"gpt-5","input":"hello"}`,
			group:       &service.Group{DisableResponsesAPI: &trueValue},
			run:         (&GatewayHandler{}).Responses,
			wantMessage: "Responses API is disabled for this group",
		},
		{
			name:        "codex_direct_responses_alias",
			method:      http.MethodPost,
			path:        "/backend-api/codex/responses",
			body:        `{"model":"gpt-5","input":"hello"}`,
			group:       &service.Group{DisableResponsesAPI: &trueValue},
			run:         (&GatewayHandler{}).Responses,
			wantMessage: "Responses API is disabled for this group",
		},
		{
			name:        "bare_chat_completions_alias",
			method:      http.MethodPost,
			path:        "/chat/completions",
			body:        `{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`,
			group:       &service.Group{DisableChatCompletionsAPI: &trueValue},
			run:         (&GatewayHandler{}).ChatCompletions,
			wantMessage: "Chat Completions API is disabled for this group",
		},
		{
			name:        "bare_responses_websocket_alias",
			method:      http.MethodGet,
			path:        "/responses",
			group:       &service.Group{DisableResponsesAPI: &trueValue},
			run:         newOpenAIHandlerForPreviousResponseIDValidation(t, nil).ResponsesWebSocket,
			wantMessage: "Responses API is disabled for this group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newEndpointDisableFlagContext(tt.method, tt.path, tt.body, tt.group)
			if tt.method == http.MethodGet {
				c.Request.Header.Set("Upgrade", "websocket")
				c.Request.Header.Set("Connection", "Upgrade")
			}

			tt.run(c)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			require.Equal(t, tt.wantMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
		})
	}
}

func newEndpointDisableFlagContext(method, path, body string, group *service.Group) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		c.Request.Header.Set("Content-Type", "application/json")
	}

	groupID := int64(7)
	if group == nil {
		group = &service.Group{}
	}
	group.ID = groupID
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      11,
		UserID:  13,
		GroupID: &groupID,
		Group:   group,
		User:    &service.User{ID: 13},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 13, Concurrency: 1})

	return c, rec
}
