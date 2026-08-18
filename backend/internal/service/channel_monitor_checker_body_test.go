//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// swapMonitorHTTPClient 临时替换 monitorHTTPClient 为不带 SSRF 校验的普通 client，
// 让 httptest (127.0.0.1) 能连通。测试结束后恢复。
func swapMonitorHTTPClient(t *testing.T) {
	t.Helper()
	orig := monitorHTTPClient
	monitorHTTPClient = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { monitorHTTPClient = orig })
}

// captureHandler 把每次收到的请求 body 和 headers 存起来，测试断言用。
type captureHandler struct {
	lastBody    map[string]any
	lastHeaders http.Header
	respondText string // 写到 Anthropic content[0].text 里（校验用）
	status      int
}

func (h *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.lastHeaders = r.Header.Clone()
	defer func() { _ = r.Body.Close() }()
	var parsed map[string]any
	_ = json.NewDecoder(r.Body).Decode(&parsed)
	h.lastBody = parsed

	if h.status == 0 {
		h.status = 200
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(h.status)
	// 构造 Anthropic 格式的响应：content[0].text = h.respondText
	_ = json.NewEncoder(w).Encode(map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": h.respondText},
		},
	})
}

func setupFakeAnthropic(t *testing.T, handler *captureHandler) string {
	t.Helper()
	swapMonitorHTTPClient(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

type openAICaptureHandler struct {
	lastBody                  map[string]any
	lastHeaders               http.Header
	lastPath                  string
	status                    int
	rawResponse               string
	responsesLeadingReasoning bool
}

func (h *openAICaptureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.lastHeaders = r.Header.Clone()
	h.lastPath = r.URL.Path
	defer func() { _ = r.Body.Close() }()
	var parsed map[string]any
	_ = json.NewDecoder(r.Body).Decode(&parsed)
	h.lastBody = parsed

	if h.status == 0 {
		h.status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(h.status)
	if h.rawResponse != "" {
		_, _ = w.Write([]byte(h.rawResponse))
		return
	}

	answer := answerFromOpenAIRequest(parsed)
	if h.lastPath == providerOpenAIResponsesPath {
		output := []map[string]any{}
		if h.responsesLeadingReasoning {
			output = append(output, map[string]any{
				"type":    "reasoning",
				"summary": []any{},
			})
		}
		output = append(output, map[string]any{
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": answer},
			},
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": output,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": answer}}},
	})
}

func setupFakeOpenAI(t *testing.T, handler *openAICaptureHandler) string {
	t.Helper()
	swapMonitorHTTPClient(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func answerFromOpenAIRequest(body map[string]any) string {
	prompt, _ := body["input"].(string)
	if prompt == "" {
		if messages, ok := body["messages"].([]any); ok && len(messages) > 0 {
			if msg, ok := messages[0].(map[string]any); ok {
				prompt, _ = msg["content"].(string)
			}
		}
	}
	return answerFromChallengePrompt(prompt)
}

var challengeQuestionRegex = regexp.MustCompile(`Q: (\d+) ([+-]) (\d+) = \?\nA:$`)

func answerFromChallengePrompt(prompt string) string {
	m := challengeQuestionRegex.FindStringSubmatch(prompt)
	if len(m) != 4 {
		return "0"
	}
	left, _ := strconv.Atoi(m[1])
	right, _ := strconv.Atoi(m[3])
	if m[2] == "+" {
		return strconv.Itoa(left + right)
	}
	return strconv.Itoa(left - right)
}

func TestRunCheckForModel_OffMode_PreservesDefaultBody(t *testing.T) {
	h := &captureHandler{respondText: "the answer is 42"}
	endpoint := setupFakeAnthropic(t, h)

	// 跑一次 off 模式（opts=nil），确认默认 body 行为未变
	_ = runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", nil)

	if h.lastBody["model"] != "claude-x" {
		t.Errorf("default body should contain model=claude-x, got %v", h.lastBody["model"])
	}
	if _, ok := h.lastBody["messages"]; !ok {
		t.Error("default body should contain messages")
	}
	if h.lastHeaders.Get("x-api-key") != "sk-fake" {
		t.Errorf("expected adapter's x-api-key header, got %q", h.lastHeaders.Get("x-api-key"))
	}
}

func TestRunCheckForModel_OpenAI_DefaultChatRequest(t *testing.T) {
	h := &openAICaptureHandler{}
	endpoint := setupFakeOpenAI(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderOpenAI, endpoint, "sk-openai", "gpt-test", nil)

	if res.Status != MonitorStatusOperational {
		t.Fatalf("default chat request should pass challenge, got status=%s message=%q", res.Status, res.Message)
	}
	if h.lastPath != providerOpenAIPath {
		t.Fatalf("expected chat completions path %q, got %q", providerOpenAIPath, h.lastPath)
	}
	if h.lastBody["model"] != "gpt-test" {
		t.Errorf("chat body should contain model=gpt-test, got %v", h.lastBody["model"])
	}
	if _, ok := h.lastBody["messages"]; !ok {
		t.Error("chat body should contain messages")
	}
	if _, ok := h.lastBody["instructions"]; ok {
		t.Error("chat body must not contain top-level instructions")
	}
	if h.lastBody["stream"] != false {
		t.Errorf("chat body should set stream=false, got %v", h.lastBody["stream"])
	}
	if h.lastHeaders.Get("Authorization") != "Bearer sk-openai" {
		t.Errorf("expected bearer auth header, got %q", h.lastHeaders.Get("Authorization"))
	}
}

func TestGrokMonitorConfiguration(t *testing.T) {
	if err := validateProvider(MonitorProviderGrok); err != nil {
		t.Fatalf("grok provider should be supported: %v", err)
	}
	if got := normalizeMonitorPrimaryModel(MonitorProviderGrok, MonitorCheckModeProbe, ""); got != MonitorDefaultGrokModel {
		t.Fatalf("expected default Grok model %q, got %q", MonitorDefaultGrokModel, got)
	}
	if err := validateAPIMode(MonitorProviderGrok, MonitorAPIModeChatCompletions); err != nil {
		t.Fatalf("grok chat_completions mode should be valid: %v", err)
	}
	if err := validateAPIMode(MonitorProviderGrok, MonitorAPIModeResponses); err == nil {
		t.Fatal("grok responses mode should be rejected by channel monitoring")
	}
	if err := validateReplaceRequestBody(MonitorProviderGrok, MonitorAPIModeChatCompletions, map[string]any{}); err == nil {
		t.Fatal("grok replace-mode body should require messages")
	}
}

func TestRunCheckForModel_Grok_DefaultChatRequest(t *testing.T) {
	h := &openAICaptureHandler{}
	endpoint := setupFakeOpenAI(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderGrok, endpoint, "xai-key", MonitorDefaultGrokModel, nil)

	if res.Status != MonitorStatusOperational {
		t.Fatalf("Grok request should pass challenge, got status=%s message=%q", res.Status, res.Message)
	}
	if res.LatencyMs == nil {
		t.Fatal("Grok request should record latency")
	}
	if h.lastPath != providerGrokPath {
		t.Fatalf("expected Grok chat completions path %q, got %q", providerGrokPath, h.lastPath)
	}
	if h.lastBody["model"] != MonitorDefaultGrokModel {
		t.Errorf("Grok body should contain model=%s, got %v", MonitorDefaultGrokModel, h.lastBody["model"])
	}
	if _, ok := h.lastBody["messages"]; !ok {
		t.Error("Grok body should contain messages")
	}
	if h.lastBody["stream"] != false {
		t.Errorf("Grok body should set stream=false, got %v", h.lastBody["stream"])
	}
	if h.lastHeaders.Get("Authorization") != "Bearer xai-key" {
		t.Errorf("expected Grok bearer auth header, got %q", h.lastHeaders.Get("Authorization"))
	}
}

func TestRunCheckForModel_Grok_UpstreamFailure(t *testing.T) {
	h := &openAICaptureHandler{status: http.StatusTooManyRequests}
	endpoint := setupFakeOpenAI(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderGrok, endpoint, "xai-key", MonitorDefaultGrokModel, nil)

	if res.Status != MonitorStatusError {
		t.Fatalf("Grok 429 should be recorded as error, got status=%s message=%q", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "upstream HTTP 429") {
		t.Fatalf("Grok failure should preserve upstream status, got %q", res.Message)
	}
	if res.LatencyMs == nil {
		t.Fatal("Grok failure should still record latency")
	}
}

func TestRunCheckForModel_Grok_RedactsXAIKeyFromUpstreamBody(t *testing.T) {
	h := &openAICaptureHandler{
		status:      http.StatusUnauthorized,
		rawResponse: `{"error":{"message":"invalid API key xai-secret"}}`,
	}
	endpoint := setupFakeOpenAI(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderGrok, endpoint, "request-key", MonitorDefaultGrokModel, nil)

	if res.Status != MonitorStatusError {
		t.Fatalf("Grok upstream failure should be recorded as error, got %s", res.Status)
	}
	if strings.Contains(res.Message, "xai-secret") {
		t.Fatalf("Grok error message leaked xAI key: %q", res.Message)
	}
	if !strings.Contains(res.Message, "xai-***REDACTED***") {
		t.Fatalf("Grok error message should contain redaction marker, got %q", res.Message)
	}
}

func TestRunCheckForModel_OpenAIResponses_DefaultRequest(t *testing.T) {
	h := &openAICaptureHandler{}
	endpoint := setupFakeOpenAI(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderOpenAI, endpoint, "sk-openai", "gpt-test", &CheckOptions{
		APIMode: MonitorAPIModeResponses,
	})

	if res.Status != MonitorStatusOperational {
		t.Fatalf("default responses request should pass challenge, got status=%s message=%q", res.Status, res.Message)
	}
	if h.lastPath != providerOpenAIResponsesPath {
		t.Fatalf("expected responses path %q, got %q", providerOpenAIResponsesPath, h.lastPath)
	}
	if h.lastBody["model"] != "gpt-test" {
		t.Errorf("responses body should contain model=gpt-test, got %v", h.lastBody["model"])
	}
	instructions, _ := h.lastBody["instructions"].(string)
	if strings.TrimSpace(instructions) == "" {
		t.Error("responses body should contain non-empty instructions")
	}
	input, _ := h.lastBody["input"].(string)
	if strings.TrimSpace(input) == "" {
		t.Error("responses body should contain non-empty input")
	}
	if _, ok := h.lastBody["messages"]; ok {
		t.Error("responses body must not contain chat messages")
	}
	if h.lastBody["stream"] != false {
		t.Errorf("responses body should set stream=false, got %v", h.lastBody["stream"])
	}
	if h.lastHeaders.Get("Authorization") != "Bearer sk-openai" {
		t.Errorf("expected bearer auth header, got %q", h.lastHeaders.Get("Authorization"))
	}
}

func TestRunCheckForModel_OpenAIResponses_SkipsLeadingReasoningItem(t *testing.T) {
	h := &openAICaptureHandler{responsesLeadingReasoning: true}
	endpoint := setupFakeOpenAI(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderOpenAI, endpoint, "sk-openai", "gpt-5.5", &CheckOptions{
		APIMode: MonitorAPIModeResponses,
	})

	if res.Status != MonitorStatusOperational {
		t.Fatalf("responses request should find text after leading reasoning item, got status=%s message=%q", res.Status, res.Message)
	}
	if h.lastPath != providerOpenAIResponsesPath {
		t.Fatalf("expected responses path %q, got %q", providerOpenAIResponsesPath, h.lastPath)
	}
}

func TestRunCheckForModel_OpenAIResponsesReplaceMissingInstructionsFailsLocally(t *testing.T) {
	h := &openAICaptureHandler{}
	endpoint := setupFakeOpenAI(t, h)

	res := runCheckForModel(context.Background(), MonitorProviderOpenAI, endpoint, "sk-openai", "gpt-test", &CheckOptions{
		APIMode:          MonitorAPIModeResponses,
		BodyOverrideMode: MonitorBodyOverrideModeReplace,
		BodyOverride: map[string]any{
			"model": "gpt-test",
			"input": "hello",
		},
	})

	if res.Status != MonitorStatusError {
		t.Fatalf("invalid responses replace body should fail locally as error, got status=%s", res.Status)
	}
	if !strings.Contains(res.Message, "instructions and input are required") {
		t.Errorf("expected local validation message about instructions/input, got %q", res.Message)
	}
	if h.lastPath != "" {
		t.Errorf("invalid replace body should fail before HTTP request, got path %q", h.lastPath)
	}
}

func TestRunCheckForModel_MergeMode_UserFieldsWinButDenyListProtects(t *testing.T) {
	h := &captureHandler{respondText: "the answer is 42"}
	endpoint := setupFakeAnthropic(t, h)

	opts := &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeMerge,
		BodyOverride: map[string]any{
			"system":     "You are Claude Code...",
			"max_tokens": float64(999),   // 应该覆盖默认 50
			"model":      "hacked-model", // 应该被黑名单挡住，保留原 model
			"messages":   []any{},        // 同上，被挡
		},
		ExtraHeaders: map[string]string{
			"User-Agent":     "claude-cli/1.0",
			"Content-Length": "999", // 黑名单
			"x-custom":       "ok",
		},
	}
	_ = runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", opts)

	if h.lastBody["system"] != "You are Claude Code..." {
		t.Errorf("merge mode should inject system, got %v", h.lastBody["system"])
	}
	// max_tokens 覆盖生效
	if mt, ok := h.lastBody["max_tokens"].(float64); !ok || mt != 999 {
		t.Errorf("merge mode should override max_tokens to 999, got %v", h.lastBody["max_tokens"])
	}
	// model 在黑名单 — 应该保留默认值
	if h.lastBody["model"] != "claude-x" {
		t.Errorf("model should be protected by deny list, got %v", h.lastBody["model"])
	}
	// messages 在黑名单 — 应该保留默认值（非空）
	msgs, _ := h.lastBody["messages"].([]any)
	if len(msgs) == 0 {
		t.Error("messages should be protected by deny list (kept default, non-empty)")
	}
	// header 合并
	if h.lastHeaders.Get("User-Agent") != "claude-cli/1.0" {
		t.Errorf("extra User-Agent should override, got %q", h.lastHeaders.Get("User-Agent"))
	}
	if h.lastHeaders.Get("x-custom") != "ok" {
		t.Errorf("extra custom header should be present, got %q", h.lastHeaders.Get("x-custom"))
	}
	// Content-Length 黑名单：会被 net/http 自动重算，但不应由用户的 "999" 决定。
	// 我们无法直接断言丢弃（http.Client 总会填上），只断言请求成功即可。
}

func TestRunCheckForModel_ReplaceMode_FullBodyUsedAndChallengeSkipped(t *testing.T) {
	// replace 模式下我们的 body 完全自定义，challenge 数学题不会出现在请求里，
	// 上游也不会回正确答案 — 但只要 2xx + 响应文本非空，就算 operational
	h := &captureHandler{respondText: "any non-empty text"}
	endpoint := setupFakeAnthropic(t, h)

	userBody := map[string]any{
		"model":      "user-forced-model",
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens": float64(10),
		"system":     "You are someone else",
	}
	opts := &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeReplace,
		BodyOverride:     userBody,
	}
	res := runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", opts)

	// 请求 body = 用户提供的原样
	if h.lastBody["model"] != "user-forced-model" {
		t.Errorf("replace mode should use user's model, got %v", h.lastBody["model"])
	}
	if h.lastBody["system"] != "You are someone else" {
		t.Errorf("replace mode should use user's system, got %v", h.lastBody["system"])
	}
	// challenge 虽然没命中，但由于 replace 模式跳过 challenge 校验 + 响应非空 → operational
	if res.Status != MonitorStatusOperational {
		t.Errorf("replace mode with 2xx + non-empty text should be operational, got status=%s message=%q",
			res.Status, res.Message)
	}
}

func TestRunCheckForModel_ReplaceMode_EmptyResponseIsFailed(t *testing.T) {
	h := &captureHandler{respondText: ""} // 上游 200 但 content[0].text 为空
	endpoint := setupFakeAnthropic(t, h)

	opts := &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeReplace,
		BodyOverride:     map[string]any{"model": "x", "messages": []any{}},
	}
	res := runCheckForModel(context.Background(), MonitorProviderAnthropic, endpoint, "sk-fake", "claude-x", opts)

	if res.Status != MonitorStatusFailed {
		t.Errorf("replace mode with empty text should be failed, got status=%s", res.Status)
	}
	if !strings.Contains(res.Message, "replace-mode") {
		t.Errorf("failure message should hint replace-mode, got %q", res.Message)
	}
}

func TestExtractAnthropicMonitorText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "text block after thinking",
			body: `{"content":[{"type":"thinking","thinking":""},{"type":"text","text":"2"}]}`,
			want: "2",
		},
		{
			name: "single text block",
			body: `{"content":[{"type":"text","text":"2"}]}`,
			want: "2",
		},
		{
			name: "thinking only",
			body: `{"content":[{"type":"thinking","thinking":""}]}`,
			want: "",
		},
		{
			name: "multiple text blocks",
			body: `{"content":[{"type":"text","text":"answer"},{"type":"tool_use","name":"x"},{"type":"text","text":"2"}]}`,
			want: "answer\n2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAnthropicMonitorText([]byte(tt.body))
			if got != tt.want {
				t.Fatalf("extractAnthropicMonitorText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateChallenge_AnthropicTextAfterThinking(t *testing.T) {
	body := []byte(`{"content":[{"type":"thinking","thinking":""},{"type":"text","text":"答案是 2"}]}`)
	respText := extractAnthropicMonitorText(body)

	if !validateChallenge(respText, "2") {
		t.Fatalf("validateChallenge(%q, %q) = false, want true", respText, "2")
	}
}

func TestRunCheckForModelWithRetry_ZeroRetriesSendsOnce(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary"}}`))
	}))
	t.Cleanup(server.Close)
	swapMonitorHTTPClient(t)

	result := runCheckForModelWithRetry(context.Background(), MonitorProviderOpenAI, server.URL, "key", "model", nil, 0)
	if result.Status != MonitorStatusError {
		t.Fatalf("expected error result, got %s", result.Status)
	}
	if requests != 1 {
		t.Fatalf("zero retries should send one request, got %d", requests)
	}
}

func TestRunCheckForModelWithRetry_RetryThenSucceeds(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary"}}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": answerFromChallengePrompt(body["messages"].([]any)[0].(map[string]any)["content"].(string)),
			}}},
		})
	}))
	t.Cleanup(server.Close)
	swapMonitorHTTPClient(t)

	result := runCheckForModelWithRetry(context.Background(), MonitorProviderOpenAI, server.URL, "key", "model", nil, 1)
	if result.Status != MonitorStatusOperational {
		t.Fatalf("expected retry to succeed, got %s: %s", result.Status, result.Message)
	}
	if requests != 2 {
		t.Fatalf("expected one retry, got %d requests", requests)
	}
}

func TestRunCheckForModelWithRetry_ExhaustsRetries(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary"}}`))
	}))
	t.Cleanup(server.Close)
	swapMonitorHTTPClient(t)

	result := runCheckForModelWithRetry(context.Background(), MonitorProviderOpenAI, server.URL, "key", "model", nil, 2)
	if result.Status != MonitorStatusError {
		t.Fatalf("expected final error after retries, got %s", result.Status)
	}
	if requests != 3 {
		t.Fatalf("expected initial request plus two retries, got %d", requests)
	}
}

func TestRunCheckForModelWithRetry_PassModeDoesNotSendRequest(t *testing.T) {
	requests := 0
	original := monitorHTTPClient
	monitorHTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("unexpected HTTP request")
	})}
	t.Cleanup(func() { monitorHTTPClient = original })

	result := runCheckForModelWithRetry(context.Background(), MonitorProviderOpenAI, "https://example.com", "key", "model", &CheckOptions{
		CheckMode:        MonitorCheckModePass,
		PassLatencyMinMs: 1800,
		PassLatencyMaxMs: 3200,
	}, 5)
	if result.Status != MonitorStatusOperational || result.Message != "" {
		t.Fatalf("pass mode should return ordinary operational result, got %#v", result)
	}
	if result.LatencyMs == nil || *result.LatencyMs < 1800 || *result.LatencyMs > 3200 {
		t.Fatalf("pass mode latency should stay in configured range, got %v", result.LatencyMs)
	}
	if result.PingLatencyMs != nil {
		t.Fatalf("pass mode must not fabricate ping latency, got %v", result.PingLatencyMs)
	}
	if requests != 0 {
		t.Fatalf("pass mode sent %d HTTP requests", requests)
	}
}

func TestRunChecksConcurrent_PassModeGeneratesSharedConfiguredPing(t *testing.T) {
	pingRequests := 0
	original := monitorPingHTTPClient
	monitorPingHTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		pingRequests++
		return nil, fmt.Errorf("unexpected ping request")
	})}
	t.Cleanup(func() { monitorPingHTTPClient = original })

	service := &ChannelMonitorService{}
	results := service.runChecksConcurrent(context.Background(), &ChannelMonitor{
		Provider:             MonitorProviderOpenAI,
		Endpoint:             "https://example.com",
		PrimaryModel:         "primary",
		ExtraModels:          []string{"extra"},
		CheckMode:            MonitorCheckModePass,
		PassLatencyMinMs:     800,
		PassLatencyMaxMs:     2500,
		PassPingLatencyMinMs: 35,
		PassPingLatencyMaxMs: 275,
	})

	if pingRequests != 0 {
		t.Fatalf("pass mode sent %d endpoint ping requests", pingRequests)
	}
	if len(results) != 2 || results[0].PingLatencyMs == nil || results[1].PingLatencyMs == nil {
		t.Fatalf("pass mode should generate ping for every model, got %#v", results)
	}
	if *results[0].PingLatencyMs < 35 || *results[0].PingLatencyMs > 275 {
		t.Fatalf("synthetic ping escaped configured range: %dms", *results[0].PingLatencyMs)
	}
	if *results[0].PingLatencyMs != *results[1].PingLatencyMs {
		t.Fatalf("models in one run should share endpoint ping: %#v", results)
	}
}

func TestRandomSyntheticLatencyMsStaysWithinConfiguredRange(t *testing.T) {
	for i := 0; i < 1000; i++ {
		latencyMs := randomSyntheticLatencyMs(1250, 4750)
		if latencyMs < 1250 || latencyMs > 4750 {
			t.Fatalf("synthetic latency escaped configured range: %dms", latencyMs)
		}
		if time.Duration(latencyMs)*time.Millisecond >= monitorDegradedThreshold {
			t.Fatalf("synthetic latency must stay below degraded threshold: %dms", latencyMs)
		}
	}
}

func TestPassCheckResultUsesDefaultsForMissingRange(t *testing.T) {
	result := passCheckResult("model", &CheckOptions{CheckMode: MonitorCheckModePass})
	if result.LatencyMs == nil || *result.LatencyMs < MonitorDefaultPassLatencyMinMs || *result.LatencyMs > MonitorDefaultPassLatencyMaxMs {
		t.Fatalf("pass mode latency should use defaults, got %v", result.LatencyMs)
	}
}

func TestPassCheckResultUsesLatencyThresholdForStatus(t *testing.T) {
	result := passCheckResult("model", &CheckOptions{
		CheckMode:        MonitorCheckModePass,
		PassLatencyMinMs: 12500,
		PassLatencyMaxMs: 12500,
	})
	if result.Status != MonitorStatusDegraded {
		t.Fatalf("pass mode should derive status from latency, got %#v", result)
	}
	if result.LatencyMs == nil || *result.LatencyMs != 12500 {
		t.Fatalf("pass mode should retain configured latency, got %v", result.LatencyMs)
	}
	if result.Message != "slow response: 12500ms" {
		t.Fatalf("pass mode should include the standard slow response message, got %q", result.Message)
	}
}

func TestValidatePassLatencyRange(t *testing.T) {
	for _, tc := range []struct {
		name    string
		minMs   int
		maxMs   int
		wantErr bool
	}{
		{name: "defaults", minMs: 0, maxMs: 0},
		{name: "configured", minMs: 1000, maxMs: 5000},
		{name: "minimum below one", minMs: -1, maxMs: 1000, wantErr: true},
		{name: "maximum below minimum", minMs: 2000, maxMs: 1000, wantErr: true},
		{name: "maximum may exceed degraded threshold", minMs: 1000, maxMs: 60000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePassLatencyRange(tc.minMs, tc.maxMs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validatePassLatencyRange(%d, %d) error = %v, wantErr %v", tc.minMs, tc.maxMs, err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeAndValidatePassPingLatencyRange(t *testing.T) {
	minMs, maxMs := normalizePassPingLatencyRange(0, 0)
	if minMs != MonitorDefaultPassPingLatencyMinMs || maxMs != MonitorDefaultPassPingLatencyMaxMs {
		t.Fatalf("unexpected default pass ping range: %d-%d", minMs, maxMs)
	}
	for i := 0; i < 1000; i++ {
		pingMs := randomSyntheticLatencyMs(35, 275)
		if pingMs < 35 || pingMs > 275 {
			t.Fatalf("synthetic ping escaped configured range: %dms", pingMs)
		}
	}
	if err := validatePassPingLatencyRange(-1, 100); err == nil {
		t.Fatal("negative pass ping minimum should be rejected")
	}
	if err := validatePassPingLatencyRange(200, 100); err == nil {
		t.Fatal("pass ping maximum below minimum should be rejected")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
