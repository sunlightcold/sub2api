package service

import "github.com/Wei-Shaw/sub2api/internal/config"

const (
	usageLatencyJitterCapMs   = 50
	usageLatencyFallbackCapMs = 50
	usageLatencyFNVOffset64   = 14695981039346656037
	usageLatencyFNVPrime64    = 1099511628211
)

func usageLatencyOffsetMs(cfg *config.Config) int {
	if cfg == nil || cfg.Gateway.UsageLatencyOffsetMs <= 0 {
		return 0
	}
	return cfg.Gateway.UsageLatencyOffsetMs
}

func (s *GatewayService) usageLatencyOffsetMs() int {
	if s == nil {
		return 0
	}
	return usageLatencyOffsetMs(s.cfg)
}

func (s *GatewayService) AdjustUsageLatencyMs(value int) int {
	return AdjustUsageLatencyMs(value, s.usageLatencyOffsetMs())
}

func (s *GatewayService) AdjustUsageLatencyPtr(value *int) *int {
	return AdjustUsageLatencyPtr(value, s.usageLatencyOffsetMs())
}

func (s *GatewayService) AdjustUsageLatencyPtrWithSeed(value *int, seed string) *int {
	return AdjustUsageLatencyPtrWithSeed(value, s.usageLatencyOffsetMs(), seed)
}

func (s *GatewayService) AdjustUsageLatencyMetrics(durationMs int, firstTokenMs *int, seed string) (int, *int) {
	return AdjustUsageLatencyMetrics(durationMs, firstTokenMs, s.usageLatencyOffsetMs(), seed)
}

func (s *OpenAIGatewayService) usageLatencyOffsetMs() int {
	if s == nil {
		return 0
	}
	return usageLatencyOffsetMs(s.cfg)
}

func (s *OpenAIGatewayService) AdjustUsageLatencyMs(value int) int {
	return AdjustUsageLatencyMs(value, s.usageLatencyOffsetMs())
}

func (s *OpenAIGatewayService) AdjustUsageLatencyPtr(value *int) *int {
	return AdjustUsageLatencyPtr(value, s.usageLatencyOffsetMs())
}

func (s *OpenAIGatewayService) AdjustUsageLatencyPtrWithSeed(value *int, seed string) *int {
	return AdjustUsageLatencyPtrWithSeed(value, s.usageLatencyOffsetMs(), seed)
}

func (s *OpenAIGatewayService) AdjustUsageLatencyMetrics(durationMs int, firstTokenMs *int, seed string) (int, *int) {
	return AdjustUsageLatencyMetrics(durationMs, firstTokenMs, s.usageLatencyOffsetMs(), seed)
}

func AdjustUsageLatencyMs(value int, offsetMs int) int {
	return AdjustUsageLatencyMsWithSeed(value, offsetMs, "")
}

func AdjustUsageLatencyPtr(value *int, offsetMs int) *int {
	if value == nil {
		return nil
	}
	adjusted := AdjustUsageLatencyMs(*value, offsetMs)
	return &adjusted
}

func AdjustUsageLatencyMsWithSeed(value int, offsetMs int, seed string) int {
	hash := usageLatencyHash(seed, "single")
	hash = usageLatencyMixInt(hash, value)
	effectiveOffset := usageLatencyEffectiveOffset(offsetMs, hash)
	return adjustUsageLatencyValue(value, effectiveOffset, usageLatencyMixString(hash, "fallback"))
}

func AdjustUsageLatencyPtrWithSeed(value *int, offsetMs int, seed string) *int {
	if value == nil {
		return nil
	}
	adjusted := AdjustUsageLatencyMsWithSeed(*value, offsetMs, seed)
	return &adjusted
}

func AdjustUsageLatencyMetrics(durationMs int, firstTokenMs *int, offsetMs int, seed string) (int, *int) {
	hash := usageLatencyHash(seed, "pair")
	hash = usageLatencyMixInt(hash, durationMs)
	if firstTokenMs != nil {
		hash = usageLatencyMixInt(hash, *firstTokenMs)
	}
	effectiveOffset := usageLatencyEffectiveOffset(offsetMs, hash)
	duration := adjustUsageLatencyValue(durationMs, effectiveOffset, usageLatencyMixString(hash, "duration"))
	if firstTokenMs == nil {
		return duration, nil
	}

	firstToken := adjustUsageLatencyValue(*firstTokenMs, effectiveOffset, usageLatencyMixString(hash, "first_token"))
	if firstToken > duration {
		firstToken = duration
	}
	return duration, &firstToken
}

func usageLatencyEffectiveOffset(offsetMs int, hash uint64) int {
	if offsetMs <= 0 {
		return 0
	}
	jitterRange := offsetMs / 10
	if jitterRange < 1 {
		jitterRange = 1
	}
	if jitterRange > usageLatencyJitterCapMs {
		jitterRange = usageLatencyJitterCapMs
	}
	jitter := int(hash%uint64(jitterRange*2+1)) - jitterRange
	effectiveOffset := offsetMs + jitter
	if effectiveOffset < 1 {
		return 1
	}
	return effectiveOffset
}

func adjustUsageLatencyValue(value int, effectiveOffsetMs int, hash uint64) int {
	if effectiveOffsetMs <= 0 {
		return value
	}
	if value <= 0 {
		return 1
	}
	adjusted := value - effectiveOffsetMs
	if adjusted > 0 {
		if adjusted > value {
			return value
		}
		return adjusted
	}

	maxFallback := value
	if maxFallback > usageLatencyFallbackCapMs {
		maxFallback = usageLatencyFallbackCapMs
	}
	return int(hash%uint64(maxFallback)) + 1
}

func usageLatencyHash(parts ...string) uint64 {
	hash := uint64(usageLatencyFNVOffset64)
	for _, part := range parts {
		hash = usageLatencyMixString(hash, part)
		hash = usageLatencyMixByte(hash, 0)
	}
	return hash
}

func usageLatencyMixString(hash uint64, value string) uint64 {
	for i := 0; i < len(value); i++ {
		hash = usageLatencyMixByte(hash, value[i])
	}
	return hash
}

func usageLatencyMixInt(hash uint64, value int) uint64 {
	return usageLatencyMixUint64(hash, uint64(int64(value)))
}

func usageLatencyMixUint64(hash uint64, value uint64) uint64 {
	for i := 0; i < 8; i++ {
		hash = usageLatencyMixByte(hash, byte(value))
		value >>= 8
	}
	return hash
}

func usageLatencyMixByte(hash uint64, value byte) uint64 {
	hash ^= uint64(value)
	hash *= usageLatencyFNVPrime64
	return hash
}
