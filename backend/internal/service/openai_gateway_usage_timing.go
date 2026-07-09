package service

import "time"

const (
	usageTimingGatewayPrepareMs     = "gateway_prepare_ms"
	usageTimingUpstreamHeadersMs    = "upstream_headers_ms"
	usageTimingUpstreamFirstSSEMs   = "upstream_first_sse_ms"
	usageTimingTTFTMs               = "ttft_ms"
	usageTimingGatewayFirstOutputMs = "gateway_first_output_ms"
	usageTimingClientTTFTMs         = "client_ttft_ms"
	usageTimingUpstreamGenerationMs = "upstream_generation_ms"
	usageTimingStreamTailMs         = "stream_tail_ms"
	usageTimingPostHeadersMs        = "post_headers_ms"
	usageTimingTotalMs              = "total_ms"
)

func setUsageTimingMs(timing UsageUpstreamTiming, key string, ms int64) {
	if timing == nil || key == "" {
		return
	}
	if ms < 0 {
		ms = 0
	}
	timing[key] = ms
}

func setUsageTimingElapsed(timing UsageUpstreamTiming, key string, start time.Time) {
	setUsageTimingMs(timing, key, time.Since(start).Milliseconds())
}

func mergeUsageTiming(dst, src UsageUpstreamTiming) {
	if dst == nil || len(src) == 0 {
		return
	}
	for key, value := range src {
		if key == "" {
			continue
		}
		dst[key] = value
	}
}
