package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func usageLatencyIntPtr(v int) *int {
	return &v
}

func TestAdjustUsageLatencyMetrics_DisabledPreservesValues(t *testing.T) {
	duration, firstToken := AdjustUsageLatencyMetrics(0, usageLatencyIntPtr(0), 0, "disabled")

	require.Equal(t, 0, duration)
	require.NotNil(t, firstToken)
	require.Equal(t, 0, *firstToken)
}

func TestAdjustUsageLatencyMetrics_AppliesStableJitter(t *testing.T) {
	durationA, firstTokenA := AdjustUsageLatencyMetrics(1500, usageLatencyIntPtr(450), 200, "stable-request")
	durationB, firstTokenB := AdjustUsageLatencyMetrics(1500, usageLatencyIntPtr(450), 200, "stable-request")

	require.Equal(t, durationA, durationB)
	require.NotNil(t, firstTokenA)
	require.NotNil(t, firstTokenB)
	require.Equal(t, *firstTokenA, *firstTokenB)
	require.GreaterOrEqual(t, durationA, 1250)
	require.LessOrEqual(t, durationA, 1350)
	require.GreaterOrEqual(t, *firstTokenA, 200)
	require.LessOrEqual(t, *firstTokenA, 300)
	require.LessOrEqual(t, *firstTokenA, durationA)
}

func TestAdjustUsageLatencyMetrics_NeverWritesZeroWhenOffsetEnabled(t *testing.T) {
	duration, firstToken := AdjustUsageLatencyMetrics(18, usageLatencyIntPtr(4), 300, "tiny-request")

	require.GreaterOrEqual(t, duration, 1)
	require.LessOrEqual(t, duration, 18)
	require.NotNil(t, firstToken)
	require.GreaterOrEqual(t, *firstToken, 1)
	require.LessOrEqual(t, *firstToken, 4)
	require.LessOrEqual(t, *firstToken, duration)
}

func TestAdjustUsageLatencyMetrics_ZeroRawValuesFallbackToOneWhenOffsetEnabled(t *testing.T) {
	duration, firstToken := AdjustUsageLatencyMetrics(0, usageLatencyIntPtr(0), 300, "zero-request")

	require.Equal(t, 1, duration)
	require.NotNil(t, firstToken)
	require.Equal(t, 1, *firstToken)
}

func TestAdjustUsageLatencyMetrics_PreservesNilFirstToken(t *testing.T) {
	duration, firstToken := AdjustUsageLatencyMetrics(100, nil, 300, "nil-first-token")

	require.GreaterOrEqual(t, duration, 1)
	require.LessOrEqual(t, duration, 50)
	require.Nil(t, firstToken)
}

func TestAdjustUsageLatencyMetrics_ClampsFirstTokenToDuration(t *testing.T) {
	duration, firstToken := AdjustUsageLatencyMetrics(3, usageLatencyIntPtr(90), 300, "first-token-greater-than-duration")

	require.GreaterOrEqual(t, duration, 1)
	require.LessOrEqual(t, duration, 3)
	require.NotNil(t, firstToken)
	require.GreaterOrEqual(t, *firstToken, 1)
	require.LessOrEqual(t, *firstToken, duration)
}
