package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAICacheReadCorrectionEnabledKey  = "openai_cache_read_correction_enabled"
	openAICacheReadRatioMinKey           = "openai_cache_read_ratio_min"
	openAICacheReadRatioMaxKey           = "openai_cache_read_ratio_max"
	openAICacheReadWarmingRatioMinKey    = "openai_cache_read_warming_ratio_min"
	openAICacheReadWarmingRatioMaxKey    = "openai_cache_read_warming_ratio_max"
	openAICacheReadColdRatioMinKey       = "openai_cache_read_cold_ratio_min"
	openAICacheReadColdRatioMaxKey       = "openai_cache_read_cold_ratio_max"
	openAICacheReadMinInputTokensKey     = "openai_cache_read_min_input_tokens"
	openAICacheReadStateTTLMinutesKey    = "openai_cache_state_ttl_minutes"
	openAICacheReadPrefixMaxHashBytesKey = "openai_cache_prefix_max_hash_bytes"

	defaultOpenAICacheReadWarmRatioMin       = 0.88
	defaultOpenAICacheReadWarmRatioMax       = 0.94
	defaultOpenAICacheReadWarmingRatioMin    = 0.35
	defaultOpenAICacheReadWarmingRatioMax    = 0.75
	defaultOpenAICacheReadColdRatioMin       = 0.0
	defaultOpenAICacheReadColdRatioMax       = 0.0
	defaultOpenAICacheReadMinInputTokens     = 1024
	defaultOpenAICacheReadStateTTL           = time.Hour
	defaultOpenAICacheReadPrefixMaxHashBytes = 1 << 20
	defaultOpenAICacheReadMonolithicFraction = 0.90
	minOpenAICacheReadStateTTL               = time.Minute
	maxOpenAICacheReadStateTTL               = 24 * time.Hour
	minOpenAICacheReadPrefixMaxHashBytes     = 4 << 10
	maxOpenAICacheReadPrefixMaxHashBytes     = 8 << 20
)

type OpenAICacheReadState struct {
	SeenCount        int   `json:"seen_count"`
	LastSeenUnix     int64 `json:"last_seen_unix"`
	LastInputTokens  int   `json:"last_input_tokens"`
	LastCachedTokens int   `json:"last_cached_tokens"`
}

type OpenAICacheReadStateCache interface {
	GetOpenAICacheReadState(ctx context.Context, key string) (*OpenAICacheReadState, error)
	SetOpenAICacheReadState(ctx context.Context, key string, state *OpenAICacheReadState, ttl time.Duration) error
}

type openAICacheReadCorrectionConfig struct {
	Enabled            bool
	WarmRatioMin       float64
	WarmRatioMax       float64
	WarmingRatioMin    float64
	WarmingRatioMax    float64
	ColdRatioMin       float64
	ColdRatioMax       float64
	MinInputTokens     int
	StateTTL           time.Duration
	PrefixMaxHashBytes int
}

type openAICacheReadCorrectionContext struct {
	config            openAICacheReadCorrectionConfig
	cache             OpenAICacheReadStateCache
	stateKey          string
	prefixHash        string
	priorSeenCount    int
	cacheableFraction float64
}

type openAICacheReadCorrectionResult struct {
	Changed              bool
	OriginalCachedTokens int
	CorrectedTokens      int
	TargetRatio          float64
	CacheableTokenCap    int
}

func (a *Account) OpenAICacheReadCorrectionConfig() openAICacheReadCorrectionConfig {
	cfg := openAICacheReadCorrectionConfig{
		WarmRatioMin:       defaultOpenAICacheReadWarmRatioMin,
		WarmRatioMax:       defaultOpenAICacheReadWarmRatioMax,
		WarmingRatioMin:    defaultOpenAICacheReadWarmingRatioMin,
		WarmingRatioMax:    defaultOpenAICacheReadWarmingRatioMax,
		ColdRatioMin:       defaultOpenAICacheReadColdRatioMin,
		ColdRatioMax:       defaultOpenAICacheReadColdRatioMax,
		MinInputTokens:     defaultOpenAICacheReadMinInputTokens,
		StateTTL:           defaultOpenAICacheReadStateTTL,
		PrefixMaxHashBytes: defaultOpenAICacheReadPrefixMaxHashBytes,
	}
	if a == nil || !a.IsOpenAIApiKey() || a.Extra == nil {
		return cfg
	}
	cfg.Enabled = a.getExtraBool(openAICacheReadCorrectionEnabledKey)
	cfg.WarmRatioMin = normalizeOpenAICacheReadRatio(a.getExtraFloat64(openAICacheReadRatioMinKey), defaultOpenAICacheReadWarmRatioMin)
	cfg.WarmRatioMax = normalizeOpenAICacheReadRatio(a.getExtraFloat64(openAICacheReadRatioMaxKey), defaultOpenAICacheReadWarmRatioMax)
	cfg.WarmingRatioMin = normalizeOpenAICacheReadRatio(a.getExtraFloat64(openAICacheReadWarmingRatioMinKey), defaultOpenAICacheReadWarmingRatioMin)
	cfg.WarmingRatioMax = normalizeOpenAICacheReadRatio(a.getExtraFloat64(openAICacheReadWarmingRatioMaxKey), defaultOpenAICacheReadWarmingRatioMax)
	cfg.ColdRatioMin = normalizeOpenAICacheReadRatio(a.getExtraFloat64(openAICacheReadColdRatioMinKey), defaultOpenAICacheReadColdRatioMin)
	cfg.ColdRatioMax = normalizeOpenAICacheReadRatio(a.getExtraFloat64(openAICacheReadColdRatioMaxKey), defaultOpenAICacheReadColdRatioMax)
	cfg.WarmRatioMin, cfg.WarmRatioMax = normalizeOpenAICacheReadRatioRange(cfg.WarmRatioMin, cfg.WarmRatioMax)
	cfg.WarmingRatioMin, cfg.WarmingRatioMax = normalizeOpenAICacheReadRatioRange(cfg.WarmingRatioMin, cfg.WarmingRatioMax)
	cfg.ColdRatioMin, cfg.ColdRatioMax = normalizeOpenAICacheReadRatioRange(cfg.ColdRatioMin, cfg.ColdRatioMax)
	if minTokens := a.getExtraInt(openAICacheReadMinInputTokensKey); minTokens > 0 {
		cfg.MinInputTokens = minTokens
	}
	if ttlMinutes := a.getExtraInt(openAICacheReadStateTTLMinutesKey); ttlMinutes > 0 {
		cfg.StateTTL = time.Duration(ttlMinutes) * time.Minute
	}
	if cfg.StateTTL < minOpenAICacheReadStateTTL {
		cfg.StateTTL = minOpenAICacheReadStateTTL
	}
	if cfg.StateTTL > maxOpenAICacheReadStateTTL {
		cfg.StateTTL = maxOpenAICacheReadStateTTL
	}
	if maxBytes := a.getExtraInt(openAICacheReadPrefixMaxHashBytesKey); maxBytes > 0 {
		cfg.PrefixMaxHashBytes = maxBytes
	}
	if cfg.PrefixMaxHashBytes < minOpenAICacheReadPrefixMaxHashBytes {
		cfg.PrefixMaxHashBytes = minOpenAICacheReadPrefixMaxHashBytes
	}
	if cfg.PrefixMaxHashBytes > maxOpenAICacheReadPrefixMaxHashBytes {
		cfg.PrefixMaxHashBytes = maxOpenAICacheReadPrefixMaxHashBytes
	}
	return cfg
}

func normalizeOpenAICacheReadRatio(value, fallback float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	if value > 1 {
		value = value / 100
	}
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func normalizeOpenAICacheReadRatioRange(minRatio, maxRatio float64) (float64, float64) {
	if minRatio < 0 {
		minRatio = 0
	}
	if maxRatio < 0 {
		maxRatio = 0
	}
	if minRatio > 1 {
		minRatio = 1
	}
	if maxRatio > 1 {
		maxRatio = 1
	}
	if minRatio > maxRatio {
		minRatio, maxRatio = maxRatio, minRatio
	}
	return minRatio, maxRatio
}

func (s *OpenAIGatewayService) prepareOpenAICacheReadCorrection(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	requestBody []byte,
	model string,
) *openAICacheReadCorrectionContext {
	if account == nil {
		return nil
	}
	cfg := account.OpenAICacheReadCorrectionConfig()
	if !cfg.Enabled {
		return nil
	}
	cache, ok := s.cache.(OpenAICacheReadStateCache)
	if !ok || cache == nil {
		return nil
	}
	prefixHash, fraction := buildOpenAICacheReadPrefixFingerprint(c, requestBody, model, cfg.PrefixMaxHashBytes)
	if prefixHash == "" {
		return nil
	}
	endpoint := "unknown"
	if c != nil && c.Request != nil && strings.TrimSpace(c.Request.URL.Path) != "" {
		endpoint = strings.TrimSpace(c.Request.URL.Path)
	}
	stateKey := fmt.Sprintf("openai_cache_read:%d:%s:%s:%s", account.ID, strings.TrimSpace(model), endpoint, prefixHash)
	var priorSeen int
	if state, err := cache.GetOpenAICacheReadState(ctx, stateKey); err == nil && state != nil {
		priorSeen = state.SeenCount
	}
	return &openAICacheReadCorrectionContext{
		config:            cfg,
		cache:             cache,
		stateKey:          stateKey,
		prefixHash:        prefixHash,
		priorSeenCount:    priorSeen,
		cacheableFraction: fraction,
	}
}

func buildOpenAICacheReadPrefixFingerprint(c *gin.Context, body []byte, model string, maxBytes int) (string, float64) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return "", 0
	}
	if strings.TrimSpace(model) == "" {
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	h := xxhash.New()
	w := &openAIPrefixHashWriter{h: h, maxBytes: maxBytes}
	w.writeString("v1|")
	if c != nil && c.Request != nil {
		w.writeString(strings.TrimSpace(c.Request.URL.Path))
	}
	w.writeString("|model:")
	w.writeString(model)

	var prefixBytes, totalBytes int
	addStable := func(label string, raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		w.writeString("|")
		w.writeString(label)
		w.writeString(":")
		w.writeRaw(raw)
		prefixBytes += len(raw)
		totalBytes += len(raw)
	}
	addMonolithicStablePrefix := func(label string, raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		totalBytes += len(raw)
		prefixLen := int(math.Floor(float64(len(raw)) * defaultOpenAICacheReadMonolithicFraction))
		if prefixLen <= 0 {
			return
		}
		if prefixLen > len(raw) {
			prefixLen = len(raw)
		}
		w.writeString("|")
		w.writeString(label)
		w.writeString(":")
		w.writeString(fmt.Sprintf("%d/%d:", prefixLen, len(raw)))
		w.writeRaw(raw[:prefixLen])
		prefixBytes += prefixLen
	}

	addStable("instructions", gjson.GetBytes(body, "instructions").Raw)
	addStable("developer", gjson.GetBytes(body, "developer").Raw)
	addStable("tools", gjson.GetBytes(body, "tools").Raw)
	addStable("response_format", gjson.GetBytes(body, "response_format").Raw)
	addStable("text", gjson.GetBytes(body, "text").Raw)
	addStable("reasoning", gjson.GetBytes(body, "reasoning").Raw)

	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		items := messages.Array()
		if len(items) == 1 {
			addMonolithicStablePrefix("messages_single", items[0].Raw)
		} else {
			prefixCount := len(items)
			if prefixCount > 0 {
				prefixCount--
			}
			w.writeString("|messages_count:")
			w.writeString(fmt.Sprintf("%d/%d", prefixCount, len(items)))
			for i, item := range items {
				raw := strings.TrimSpace(item.Raw)
				if i < prefixCount {
					w.writeString("|msg:")
					w.writeRaw(raw)
					prefixBytes += len(raw)
				}
				totalBytes += len(raw)
			}
		}
	}

	if input := gjson.GetBytes(body, "input"); input.Exists() {
		if input.IsArray() {
			items := input.Array()
			if len(items) == 1 {
				addMonolithicStablePrefix("input_single", items[0].Raw)
			} else {
				prefixCount := len(items)
				if prefixCount > 0 {
					prefixCount--
				}
				w.writeString("|input_count:")
				w.writeString(fmt.Sprintf("%d/%d", prefixCount, len(items)))
				for i, item := range items {
					raw := strings.TrimSpace(item.Raw)
					if i < prefixCount {
						w.writeString("|input:")
						w.writeRaw(raw)
						prefixBytes += len(raw)
					}
					totalBytes += len(raw)
				}
			}
		} else {
			addMonolithicStablePrefix("input", input.Raw)
		}
	}

	if totalBytes <= 0 || prefixBytes <= 0 {
		return fmt.Sprintf("%016x", h.Sum64()), 0
	}
	fraction := float64(prefixBytes) / float64(totalBytes)
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	return fmt.Sprintf("%016x", h.Sum64()), fraction
}

type openAIPrefixHashWriter struct {
	h        *xxhash.Digest
	written  int
	maxBytes int
	capped   bool
}

func (w *openAIPrefixHashWriter) writeString(value string) {
	w.writeRaw(value)
}

func (w *openAIPrefixHashWriter) writeRaw(value string) {
	if value == "" || w == nil || w.h == nil {
		return
	}
	remaining := w.maxBytes - w.written
	if remaining <= 0 {
		if !w.capped {
			_, _ = w.h.WriteString("|capped")
			w.capped = true
		}
		return
	}
	if len(value) > remaining {
		value = value[:remaining]
		w.capped = true
	}
	_, _ = w.h.WriteString(value)
	w.written += len(value)
	if w.capped {
		_, _ = w.h.WriteString("|capped")
	}
}

func (s *OpenAIGatewayService) correctOpenAICacheReadUsage(
	ctx context.Context,
	account *Account,
	correction *openAICacheReadCorrectionContext,
	usage *OpenAIUsage,
	requestID string,
) openAICacheReadCorrectionResult {
	if correction == nil || usage == nil {
		return openAICacheReadCorrectionResult{}
	}
	originalCached := usage.CacheReadInputTokens
	result := correction.correctUsage(usage, requestID)
	s.updateOpenAICacheReadState(ctx, account, correction, usage.InputTokens, usage.CacheReadInputTokens)
	result.OriginalCachedTokens = originalCached
	return result
}

func (c *openAICacheReadCorrectionContext) correctUsage(usage *OpenAIUsage, requestID string) openAICacheReadCorrectionResult {
	if c == nil || usage == nil || usage.InputTokens < c.config.MinInputTokens || usage.InputTokens <= 0 {
		return openAICacheReadCorrectionResult{}
	}
	cacheableCap := int(math.Ceil(float64(usage.InputTokens) * c.cacheableFraction))
	if cacheableCap <= 0 {
		return openAICacheReadCorrectionResult{CacheableTokenCap: 0}
	}
	targetRatio := c.targetRatio(usage.InputTokens, requestID)
	if targetRatio <= 0 {
		return openAICacheReadCorrectionResult{TargetRatio: targetRatio, CacheableTokenCap: cacheableCap}
	}
	targetTokens := int(math.Ceil(float64(usage.InputTokens) * targetRatio))
	if targetTokens > cacheableCap {
		targetTokens = cacheableCap
	}
	if targetTokens > usage.InputTokens {
		targetTokens = usage.InputTokens
	}
	if targetTokens <= usage.CacheReadInputTokens {
		return openAICacheReadCorrectionResult{TargetRatio: targetRatio, CacheableTokenCap: cacheableCap}
	}
	usage.CacheReadInputTokens = targetTokens
	return openAICacheReadCorrectionResult{
		Changed:           true,
		CorrectedTokens:   targetTokens,
		TargetRatio:       targetRatio,
		CacheableTokenCap: cacheableCap,
	}
}

func (c *openAICacheReadCorrectionContext) targetRatio(inputTokens int, requestID string) float64 {
	minRatio, maxRatio := c.config.ColdRatioMin, c.config.ColdRatioMax
	if c.priorSeenCount == 1 {
		minRatio, maxRatio = c.config.WarmingRatioMin, c.config.WarmingRatioMax
	} else if c.priorSeenCount >= 2 {
		minRatio, maxRatio = c.config.WarmRatioMin, c.config.WarmRatioMax
	}
	minRatio, maxRatio = normalizeOpenAICacheReadRatioRange(minRatio, maxRatio)
	if maxRatio <= 0 {
		return 0
	}
	if minRatio == maxRatio {
		return minRatio
	}
	seed := fmt.Sprintf("%s|%s|%d|%d", c.prefixHash, requestID, c.priorSeenCount, inputTokens)
	n := xxhash.Sum64String(seed)
	unit := float64(n%10000) / 9999.0
	return minRatio + (maxRatio-minRatio)*unit
}

func (s *OpenAIGatewayService) updateOpenAICacheReadState(ctx context.Context, account *Account, correction *openAICacheReadCorrectionContext, inputTokens, cachedTokens int) {
	if correction == nil || correction.cache == nil || correction.stateKey == "" {
		return
	}
	if inputTokens <= 0 || inputTokens < correction.config.MinInputTokens {
		return
	}
	next := &OpenAICacheReadState{
		SeenCount:        correction.priorSeenCount + 1,
		LastSeenUnix:     time.Now().Unix(),
		LastInputTokens:  inputTokens,
		LastCachedTokens: cachedTokens,
	}
	if err := correction.cache.SetOpenAICacheReadState(ctx, correction.stateKey, next, correction.config.StateTTL); err != nil && account != nil {
		logger.LegacyPrintf("service.openai_gateway", "openai_cache_read_state_write_failed account=%d err=%v", account.ID, err)
	}
}

func (s *OpenAIGatewayService) correctOpenAICacheReadResponseBody(
	ctx context.Context,
	account *Account,
	correction *openAICacheReadCorrectionContext,
	body []byte,
	requestID string,
) ([]byte, *OpenAIUsage, bool) {
	if correction == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return body, nil, false
	}
	usagePath := "usage"
	usageResult := gjson.GetBytes(body, usagePath)
	if !usageResult.Exists() || !usageResult.IsObject() {
		usagePath = "response.usage"
		usageResult = gjson.GetBytes(body, usagePath)
	}
	usage, ok := openAIUsageFromGJSON(usageResult)
	if !ok {
		return body, nil, false
	}
	correctedUsage := usage
	result := s.correctOpenAICacheReadUsage(ctx, account, correction, &correctedUsage, requestID)
	if !result.Changed {
		return body, &correctedUsage, false
	}
	updated := setOpenAICachedTokensInResponseBody(body, usagePath, correctedUsage)
	return updated, &correctedUsage, !bytes.Equal(updated, body)
}

func setOpenAICachedTokensInResponseBody(body []byte, usagePath string, usage OpenAIUsage) []byte {
	if usagePath == "" {
		usagePath = "usage"
	}
	detailPath := usagePath + ".input_tokens_details.cached_tokens"
	if !gjson.GetBytes(body, usagePath+".input_tokens").Exists() && gjson.GetBytes(body, usagePath+".prompt_tokens").Exists() {
		detailPath = usagePath + ".prompt_tokens_details.cached_tokens"
	}
	updated, err := sjson.SetBytes(body, detailPath, usage.CacheReadInputTokens)
	if err != nil {
		return body
	}
	return updated
}

func (s *OpenAIGatewayService) correctOpenAICacheReadUsageOnly(
	ctx context.Context,
	account *Account,
	correction *openAICacheReadCorrectionContext,
	usage *OpenAIUsage,
	requestID string,
) {
	if correction == nil || usage == nil {
		return
	}
	s.correctOpenAICacheReadUsage(ctx, account, correction, usage, requestID)
}

func (s *OpenAIGatewayService) correctOpenAICacheReadSSEBody(
	ctx context.Context,
	account *Account,
	correction *openAICacheReadCorrectionContext,
	body string,
	requestID string,
	usage *OpenAIUsage,
) (string, *OpenAIUsage) {
	if correction == nil || strings.TrimSpace(body) == "" {
		return body, usage
	}
	lines := strings.Split(body, "\n")
	var correctedUsage *OpenAIUsage
	for i, line := range lines {
		data, ok := extractOpenAISSEDataLine(line)
		if !ok || !openAIStreamEventIsTerminal(data) {
			continue
		}
		updated, nextUsage, changed := s.correctOpenAICacheReadResponseBody(ctx, account, correction, []byte(data), requestID)
		if nextUsage != nil {
			correctedUsage = nextUsage
		}
		if changed {
			lines[i] = "data: " + string(updated)
		}
	}
	if correctedUsage != nil {
		return strings.Join(lines, "\n"), correctedUsage
	}
	return body, usage
}
