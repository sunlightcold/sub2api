package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
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
	minOpenAICacheReadStateTTL               = time.Minute
	maxOpenAICacheReadStateTTL               = 24 * time.Hour
	minOpenAICacheReadPrefixMaxHashBytes     = 4 << 10
	maxOpenAICacheReadPrefixMaxHashBytes     = 8 << 20
	openAICacheReadRoutePrefixBytes          = 1024
	openAICacheReadCandidateMinBytes         = 1024
	openAICacheReadCandidateStepBytes        = 2048
	openAICacheReadMaxStatePrefixes          = 128
)

type OpenAICacheReadState struct {
	SeenCount        int                          `json:"seen_count"`
	LastSeenUnix     int64                        `json:"last_seen_unix"`
	LastInputTokens  int                          `json:"last_input_tokens"`
	LastCachedTokens int                          `json:"last_cached_tokens"`
	Prefixes         []OpenAICacheReadPrefixState `json:"prefixes,omitempty"`
}

type OpenAICacheReadPrefixState struct {
	Hash             string `json:"hash"`
	Bytes            int    `json:"bytes"`
	SeenCount        int    `json:"seen_count"`
	LastSeenUnix     int64  `json:"last_seen_unix"`
	LastInputTokens  int    `json:"last_input_tokens"`
	LastCachedTokens int    `json:"last_cached_tokens"`
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
	promptProfile     openAICacheReadPromptProfile
	state             *OpenAICacheReadState
	matchedPrefix     *OpenAICacheReadPrefixState
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
	profile := buildOpenAICacheReadPromptProfile(c, requestBody, model, cfg.PrefixMaxHashBytes)
	if profile.RouteHash == "" || len(profile.Candidates) == 0 {
		return nil
	}
	endpoint := "unknown"
	if c != nil && c.Request != nil && strings.TrimSpace(c.Request.URL.Path) != "" {
		endpoint = strings.TrimSpace(c.Request.URL.Path)
	}
	stateKey := fmt.Sprintf("openai_cache_read:v2:%d:%s:%s:%s", account.ID, strings.TrimSpace(model), endpoint, profile.RouteHash)
	var priorSeen int
	var fraction float64
	var matched *OpenAICacheReadPrefixState
	var priorState *OpenAICacheReadState
	if loadedState, err := cache.GetOpenAICacheReadState(ctx, stateKey); err == nil && loadedState != nil {
		priorState = loadedState
		priorSeen = loadedState.SeenCount
		matched = findOpenAICacheReadBestPrefixMatch(profile.Candidates, loadedState.Prefixes)
		if matched != nil && profile.TotalBytes > 0 {
			fraction = float64(matched.Bytes) / float64(profile.TotalBytes)
			if fraction < 0 {
				fraction = 0
			}
			if fraction > 1 {
				fraction = 1
			}
		}
	}
	prefixHash := profile.RouteHash
	if matched != nil {
		prefixHash = matched.Hash
	} else if len(profile.Candidates) > 0 {
		prefixHash = profile.Candidates[len(profile.Candidates)-1].Hash
	}
	return &openAICacheReadCorrectionContext{
		config:            cfg,
		cache:             cache,
		stateKey:          stateKey,
		prefixHash:        prefixHash,
		priorSeenCount:    priorSeen,
		cacheableFraction: fraction,
		promptProfile:     profile,
		state:             priorState,
		matchedPrefix:     matched,
	}
}

func buildOpenAICacheReadPrefixFingerprint(c *gin.Context, body []byte, model string, maxBytes int) (string, float64) {
	profile := buildOpenAICacheReadPromptProfile(c, body, model, maxBytes)
	if profile.RouteHash == "" {
		return "", 0
	}
	if len(profile.Candidates) == 0 || profile.TotalBytes <= 0 {
		return profile.RouteHash, 0
	}
	last := profile.Candidates[len(profile.Candidates)-1]
	fraction := float64(last.Bytes) / float64(profile.TotalBytes)
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	return last.Hash, fraction
}

type openAICacheReadPromptProfile struct {
	RouteHash  string
	TotalBytes int
	Candidates []openAICacheReadPrefixCandidate
}

type openAICacheReadPrefixCandidate struct {
	Hash  string
	Bytes int
}

func buildOpenAICacheReadPromptProfile(c *gin.Context, body []byte, model string, maxBytes int) openAICacheReadPromptProfile {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return openAICacheReadPromptProfile{}
	}
	if strings.TrimSpace(model) == "" {
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	if maxBytes <= 0 {
		maxBytes = defaultOpenAICacheReadPrefixMaxHashBytes
	}
	if maxBytes < minOpenAICacheReadPrefixMaxHashBytes {
		maxBytes = minOpenAICacheReadPrefixMaxHashBytes
	}
	if maxBytes > maxOpenAICacheReadPrefixMaxHashBytes {
		maxBytes = maxOpenAICacheReadPrefixMaxHashBytes
	}
	builder := &strings.Builder{}
	builder.Grow(minOpenAICacheReadInt(maxBytes, len(body)+128))
	var totalBytes int
	var boundaries []int
	addPart := func(label string, raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		part := label + ":" + raw + "\n"
		totalBytes += len(part)
		if builder.Len() < maxBytes {
			remaining := maxBytes - builder.Len()
			if len(part) > remaining {
				builder.WriteString(part[:remaining])
			} else {
				builder.WriteString(part)
				boundaries = append(boundaries, builder.Len())
			}
		}
	}

	addPart("model", model)
	addPart("instructions", gjson.GetBytes(body, "instructions").Raw)
	addPart("developer", gjson.GetBytes(body, "developer").Raw)
	addPart("tools", gjson.GetBytes(body, "tools").Raw)
	addPart("response_format", gjson.GetBytes(body, "response_format").Raw)
	addPart("text", gjson.GetBytes(body, "text").Raw)
	addPart("reasoning", gjson.GetBytes(body, "reasoning").Raw)

	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		items := messages.Array()
		for i, item := range items {
			addPart(fmt.Sprintf("message[%d]", i), item.Raw)
		}
	}

	if input := gjson.GetBytes(body, "input"); input.Exists() {
		if input.IsArray() {
			items := input.Array()
			for i, item := range items {
				addPart(fmt.Sprintf("input[%d]", i), item.Raw)
			}
		} else {
			addPart("input", input.Raw)
		}
	}

	canonical := []byte(builder.String())
	if totalBytes <= 0 || len(canonical) == 0 {
		return openAICacheReadPromptProfile{}
	}
	routeLen := minOpenAICacheReadInt(openAICacheReadRoutePrefixBytes, len(canonical))
	routeHash := fmt.Sprintf("%016x", xxhash.Sum64(canonical[:routeLen]))
	positions := openAICacheReadCandidatePositions(len(canonical), boundaries)
	candidates := make([]openAICacheReadPrefixCandidate, 0, len(positions))
	for _, pos := range positions {
		if pos <= 0 || pos > len(canonical) {
			continue
		}
		candidates = append(candidates, openAICacheReadPrefixCandidate{
			Hash:  fmt.Sprintf("%016x", xxhash.Sum64(canonical[:pos])),
			Bytes: pos,
		})
	}
	return openAICacheReadPromptProfile{
		RouteHash:  routeHash,
		TotalBytes: totalBytes,
		Candidates: candidates,
	}
}

func openAICacheReadCandidatePositions(canonicalBytes int, boundaries []int) []int {
	if canonicalBytes <= 0 {
		return nil
	}
	seen := make(map[int]struct{})
	var positions []int
	add := func(pos int) {
		if pos < openAICacheReadCandidateMinBytes {
			return
		}
		if pos > canonicalBytes {
			pos = canonicalBytes
		}
		if _, ok := seen[pos]; ok {
			return
		}
		seen[pos] = struct{}{}
		positions = append(positions, pos)
	}
	for pos := openAICacheReadCandidateMinBytes; pos <= canonicalBytes; pos += openAICacheReadCandidateStepBytes {
		add(pos)
	}
	for _, pos := range boundaries {
		add(pos)
	}
	add(canonicalBytes)
	sort.Ints(positions)
	if len(positions) <= openAICacheReadMaxStatePrefixes {
		return positions
	}
	downsampled := make([]int, 0, openAICacheReadMaxStatePrefixes)
	lastIdx := -1
	for i := 0; i < openAICacheReadMaxStatePrefixes; i++ {
		idx := int(math.Round(float64(i) * float64(len(positions)-1) / float64(openAICacheReadMaxStatePrefixes-1)))
		if idx == lastIdx {
			continue
		}
		downsampled = append(downsampled, positions[idx])
		lastIdx = idx
	}
	return downsampled
}

func findOpenAICacheReadBestPrefixMatch(candidates []openAICacheReadPrefixCandidate, prefixes []OpenAICacheReadPrefixState) *OpenAICacheReadPrefixState {
	if len(candidates) == 0 || len(prefixes) == 0 {
		return nil
	}
	byKey := make(map[string]OpenAICacheReadPrefixState, len(prefixes))
	for _, prefix := range prefixes {
		if prefix.Hash == "" || prefix.Bytes <= 0 {
			continue
		}
		byKey[openAICacheReadPrefixKey(prefix.Hash, prefix.Bytes)] = prefix
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		if match, ok := byKey[openAICacheReadPrefixKey(candidate.Hash, candidate.Bytes)]; ok {
			return &match
		}
	}
	return nil
}

func openAICacheReadPrefixKey(hash string, bytes int) string {
	return fmt.Sprintf("%s:%d", hash, bytes)
}

func minOpenAICacheReadInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
	now := time.Now().Unix()
	next := &OpenAICacheReadState{
		SeenCount:        correction.priorSeenCount + 1,
		LastSeenUnix:     now,
		LastInputTokens:  inputTokens,
		LastCachedTokens: cachedTokens,
		Prefixes:         mergeOpenAICacheReadStatePrefixes(correction.state, correction.promptProfile.Candidates, inputTokens, cachedTokens, now),
	}
	if err := correction.cache.SetOpenAICacheReadState(ctx, correction.stateKey, next, correction.config.StateTTL); err != nil && account != nil {
		logger.LegacyPrintf("service.openai_gateway", "openai_cache_read_state_write_failed account=%d err=%v", account.ID, err)
	}
}

func mergeOpenAICacheReadStatePrefixes(
	state *OpenAICacheReadState,
	candidates []openAICacheReadPrefixCandidate,
	inputTokens int,
	cachedTokens int,
	now int64,
) []OpenAICacheReadPrefixState {
	merged := make(map[string]OpenAICacheReadPrefixState)
	if state != nil {
		for _, prefix := range state.Prefixes {
			if prefix.Hash == "" || prefix.Bytes <= 0 {
				continue
			}
			merged[openAICacheReadPrefixKey(prefix.Hash, prefix.Bytes)] = prefix
		}
	}
	for _, candidate := range candidates {
		if candidate.Hash == "" || candidate.Bytes <= 0 {
			continue
		}
		key := openAICacheReadPrefixKey(candidate.Hash, candidate.Bytes)
		prefix := merged[key]
		prefix.Hash = candidate.Hash
		prefix.Bytes = candidate.Bytes
		prefix.SeenCount++
		prefix.LastSeenUnix = now
		prefix.LastInputTokens = inputTokens
		prefix.LastCachedTokens = cachedTokens
		merged[key] = prefix
	}
	prefixes := make([]OpenAICacheReadPrefixState, 0, len(merged))
	for _, prefix := range merged {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if prefixes[i].LastSeenUnix != prefixes[j].LastSeenUnix {
			return prefixes[i].LastSeenUnix > prefixes[j].LastSeenUnix
		}
		return prefixes[i].Bytes > prefixes[j].Bytes
	})
	if len(prefixes) > openAICacheReadMaxStatePrefixes {
		prefixes = prefixes[:openAICacheReadMaxStatePrefixes]
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return prefixes[i].Bytes < prefixes[j].Bytes
	})
	return prefixes
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
