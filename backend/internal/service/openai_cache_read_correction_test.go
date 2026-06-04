package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAICacheReadStateCacheStub struct {
	states map[string]*OpenAICacheReadState
	gets   int
	sets   int
}

func (s *openAICacheReadStateCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, context.Canceled
}

func (s *openAICacheReadStateCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func (s *openAICacheReadStateCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *openAICacheReadStateCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (s *openAICacheReadStateCacheStub) GetOpenAICacheReadState(_ context.Context, key string) (*OpenAICacheReadState, error) {
	s.gets++
	if s.states == nil {
		return nil, context.Canceled
	}
	state, ok := s.states[key]
	if !ok {
		return nil, context.Canceled
	}
	cloned := *state
	return &cloned, nil
}

func (s *openAICacheReadStateCacheStub) SetOpenAICacheReadState(_ context.Context, key string, state *OpenAICacheReadState, _ time.Duration) error {
	s.sets++
	if s.states == nil {
		s.states = make(map[string]*OpenAICacheReadState)
	}
	cloned := *state
	s.states[key] = &cloned
	return nil
}

func openAICacheReadTestInputBody(parts ...string) []byte {
	var b strings.Builder
	_, _ = b.WriteString(`{"model":"gpt-5.4","input":[`)
	for i, part := range parts {
		if i > 0 {
			_ = b.WriteByte(',')
		}
		_, _ = b.WriteString(`{"type":"message","role":"user","content":"`)
		_, _ = b.WriteString(part)
		_, _ = b.WriteString(`"}`)
	}
	_, _ = b.WriteString(`]}`)
	return []byte(b.String())
}

func TestOpenAICacheReadCorrection_DefaultDisabledDoesNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{}}
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"stable"},{"role":"user","content":"new"}]}`)

	correction := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.Nil(t, correction)
	require.Zero(t, cache.gets)
	require.Zero(t, cache.sets)

	usage := &OpenAIUsage{InputTokens: 10000, OutputTokens: 10, CacheReadInputTokens: 100}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, correction, usage, "req_1")
	require.Equal(t, 100, usage.CacheReadInputTokens)
	require.Zero(t, cache.sets)
}

func TestOpenAICacheReadCorrection_MissingUsageDoesNotChangeBodyOrState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:       2,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openAICacheReadCorrectionEnabledKey: true,
		},
	}
	requestBody := openAICacheReadTestInputBody(strings.Repeat("stable-", 300))
	responseBody := []byte(`{"id":"resp_1","output":[{"type":"message"}]}`)

	correction := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, requestBody, "gpt-5.4")
	require.NotNil(t, correction)

	correctedBody, correctedUsage, changed := svc.correctOpenAICacheReadResponseBody(context.Background(), account, correction, responseBody, "req_missing_usage")
	require.False(t, changed)
	require.Nil(t, correctedUsage)
	require.Equal(t, string(responseBody), string(correctedBody))
	require.Zero(t, cache.sets)
}

func TestOpenAICacheReadCorrection_SkipsStateForSmallUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:       3,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openAICacheReadCorrectionEnabledKey: true,
			openAICacheReadMinInputTokensKey:    1024,
		},
	}
	body := openAICacheReadTestInputBody(strings.Repeat("stable-", 300))

	correction := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.NotNil(t, correction)

	usage := &OpenAIUsage{InputTokens: 128, OutputTokens: 10, CacheReadInputTokens: 0}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, correction, usage, "req_small")
	require.Equal(t, 0, usage.CacheReadInputTokens)
	require.Zero(t, cache.sets)
}

func TestOpenAICacheReadCorrection_DoesNotLowerNormalUpstreamCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:       4,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openAICacheReadCorrectionEnabledKey: true,
			openAICacheReadRatioMinKey:          0.88,
			openAICacheReadRatioMaxKey:          0.94,
		},
	}
	body := openAICacheReadTestInputBody(strings.Repeat("stable-", 600), strings.Repeat("tail-", 200))

	first := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.NotNil(t, first)
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, first, &OpenAIUsage{InputTokens: 10000, OutputTokens: 10}, "req_first")
	second := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.NotNil(t, second)
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, second, &OpenAIUsage{InputTokens: 10000, OutputTokens: 10}, "req_second")
	third := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.NotNil(t, third)

	usage := &OpenAIUsage{InputTokens: 10000, OutputTokens: 10, CacheReadInputTokens: 9900}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, third, usage, "req_normal")
	require.Equal(t, 9900, usage.CacheReadInputTokens)
}

func TestOpenAICacheReadCorrection_SingleInputObjectProducesCacheableFraction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:       5,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openAICacheReadCorrectionEnabledKey: true,
			openAICacheReadRatioMinKey:          0.88,
			openAICacheReadRatioMaxKey:          0.94,
		},
	}
	body := []byte(`{"model":"gpt-5.4","input":{"type":"message","role":"user","content":"` + strings.Repeat("x", 2000) + `"}}`)

	correction := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.NotNil(t, correction)
	require.Zero(t, correction.cacheableFraction)
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, correction, &OpenAIUsage{InputTokens: 2000, OutputTokens: 10}, "req_first")

	correction = svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.NotNil(t, correction)
	require.Equal(t, 1, correction.priorSeenCount)
	require.Greater(t, correction.cacheableFraction, 0.0)

	usage := &OpenAIUsage{InputTokens: 2000, OutputTokens: 10, CacheReadInputTokens: 10}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, correction, usage, "req_single_input")
	require.Greater(t, usage.CacheReadInputTokens, 10)
}

func TestOpenAICacheReadCorrection_AppendedResponsesInputMatchesPriorPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openAICacheReadCorrectionEnabledKey: true,
			openAICacheReadRatioMinKey:          0.90,
			openAICacheReadRatioMaxKey:          0.92,
			openAICacheReadMinInputTokensKey:    1024,
		},
	}
	firstBody := openAICacheReadTestInputBody(strings.Repeat("stable-", 700))
	secondBody := openAICacheReadTestInputBody(strings.Repeat("stable-", 700), strings.Repeat("next-", 180))
	thirdBody := openAICacheReadTestInputBody(strings.Repeat("stable-", 700), strings.Repeat("next-", 180), strings.Repeat("final-", 180))

	first := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, firstBody, "gpt-5.4")
	require.NotNil(t, first)
	require.Equal(t, 0, first.priorSeenCount)
	usage := &OpenAIUsage{InputTokens: 10000, OutputTokens: 10, CacheReadInputTokens: 100}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, first, usage, "req_first")
	require.Equal(t, 100, usage.CacheReadInputTokens, "cold default range is 0, so first sighting should not inflate cache")

	second := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, secondBody, "gpt-5.4")
	require.NotNil(t, second)
	require.Equal(t, 1, second.priorSeenCount)
	require.NotNil(t, second.matchedPrefix)
	require.Greater(t, second.cacheableFraction, 0.0)
	warmingUsage := &OpenAIUsage{InputTokens: 10000, OutputTokens: 10, CacheReadInputTokens: 100}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, second, warmingUsage, "req_second")
	require.GreaterOrEqual(t, warmingUsage.CacheReadInputTokens, 3500)
	require.LessOrEqual(t, warmingUsage.CacheReadInputTokens, 7500)

	third := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, thirdBody, "gpt-5.4")
	require.NotNil(t, third)
	require.Equal(t, 2, third.priorSeenCount)
	require.NotNil(t, third.matchedPrefix)
	warmUsage := &OpenAIUsage{InputTokens: 10000, OutputTokens: 10, CacheReadInputTokens: 100}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, third, warmUsage, "req_third")
	require.GreaterOrEqual(t, warmUsage.CacheReadInputTokens, 9000)
	require.LessOrEqual(t, warmUsage.CacheReadInputTokens, 9200)
	require.GreaterOrEqual(t, cache.sets, 3)
}

func TestOpenAICacheReadCorrection_WarmRatioCanExceedMatchedPrefixFraction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:       12,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openAICacheReadCorrectionEnabledKey: true,
			openAICacheReadRatioMinKey:          0.90,
			openAICacheReadRatioMaxKey:          0.90,
			openAICacheReadWarmingRatioMinKey:   0.90,
			openAICacheReadWarmingRatioMaxKey:   0.90,
		},
	}
	firstBody := openAICacheReadTestInputBody(strings.Repeat("stable-", 700))
	secondBody := openAICacheReadTestInputBody(strings.Repeat("stable-", 700), strings.Repeat("new-", 1000))

	first := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, firstBody, "gpt-5.4")
	require.NotNil(t, first)
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, first, &OpenAIUsage{InputTokens: 10000, OutputTokens: 10}, "req_first")

	second := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, secondBody, "gpt-5.4")
	require.NotNil(t, second)
	require.NotNil(t, second.matchedPrefix)
	require.Less(t, second.cacheableFraction, 0.90)

	usage := &OpenAIUsage{InputTokens: 10000, OutputTokens: 10, CacheReadInputTokens: 100}
	svc.correctOpenAICacheReadUsageOnly(context.Background(), account, second, usage, "req_second")
	require.Equal(t, 9000, usage.CacheReadInputTokens)
}

func TestOpenAICacheReadCorrection_ShortPromptDoesNotCreateCorrection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &openAICacheReadStateCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	account := &Account{
		ID:       13,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openAICacheReadCorrectionEnabledKey: true,
		},
	}
	body := []byte(`{"model":"gpt-5.4","input":"short prompt"}`)

	correction := svc.prepareOpenAICacheReadCorrection(context.Background(), nil, account, body, "gpt-5.4")
	require.Nil(t, correction)
	require.Zero(t, cache.gets)
	require.Zero(t, cache.sets)
}
