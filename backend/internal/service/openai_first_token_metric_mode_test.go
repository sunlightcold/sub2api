package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIFirstTokenMetricMode(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{name: "nil defaults to first output", raw: nil, want: OpenAIFirstTokenMetricModeFirstOutput},
		{name: "empty defaults to first output", raw: "", want: OpenAIFirstTokenMetricModeFirstOutput},
		{name: "explicit first response", raw: OpenAIFirstTokenMetricModeFirstResponse, want: OpenAIFirstTokenMetricModeFirstResponse},
		{name: "first output", raw: OpenAIFirstTokenMetricModeFirstOutput, want: OpenAIFirstTokenMetricModeFirstOutput},
		{name: "first output trims spaces", raw: "  first_output  ", want: OpenAIFirstTokenMetricModeFirstOutput},
		{name: "invalid defaults to first output", raw: "legacy", want: OpenAIFirstTokenMetricModeFirstOutput},
		{name: "non string defaults to first output", raw: 1, want: OpenAIFirstTokenMetricModeFirstOutput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NormalizeOpenAIFirstTokenMetricMode(tt.raw))
		})
	}
}

func TestAccountOpenAIFirstTokenMetricModeFromExtra(t *testing.T) {
	require.Equal(t, OpenAIFirstTokenMetricModeFirstOutput, (*Account)(nil).OpenAIFirstTokenMetricMode())
	require.False(t, (*Account)(nil).UseOpenAIFirstResponseTTFT())

	account := &Account{Extra: map[string]any{
		OpenAIFirstTokenMetricModeExtraKey: OpenAIFirstTokenMetricModeFirstResponse,
	}}
	require.Equal(t, OpenAIFirstTokenMetricModeFirstResponse, account.OpenAIFirstTokenMetricMode())
	require.True(t, account.UseOpenAIFirstResponseTTFT())

	account.Extra[OpenAIFirstTokenMetricModeExtraKey] = "invalid"
	require.Equal(t, OpenAIFirstTokenMetricModeFirstOutput, account.OpenAIFirstTokenMetricMode())
	require.False(t, account.UseOpenAIFirstResponseTTFT())
}
