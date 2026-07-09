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
		{name: "nil defaults to first response", raw: nil, want: OpenAIFirstTokenMetricModeFirstResponse},
		{name: "empty defaults to first response", raw: "", want: OpenAIFirstTokenMetricModeFirstResponse},
		{name: "explicit first response", raw: OpenAIFirstTokenMetricModeFirstResponse, want: OpenAIFirstTokenMetricModeFirstResponse},
		{name: "first output", raw: OpenAIFirstTokenMetricModeFirstOutput, want: OpenAIFirstTokenMetricModeFirstOutput},
		{name: "first output trims spaces", raw: "  first_output  ", want: OpenAIFirstTokenMetricModeFirstOutput},
		{name: "invalid defaults to first response", raw: "legacy", want: OpenAIFirstTokenMetricModeFirstResponse},
		{name: "non string defaults to first response", raw: 1, want: OpenAIFirstTokenMetricModeFirstResponse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NormalizeOpenAIFirstTokenMetricMode(tt.raw))
		})
	}
}

func TestAccountOpenAIFirstTokenMetricModeFromExtra(t *testing.T) {
	require.Equal(t, OpenAIFirstTokenMetricModeFirstResponse, (*Account)(nil).OpenAIFirstTokenMetricMode())
	require.True(t, (*Account)(nil).UseOpenAIFirstResponseTTFT())

	account := &Account{Extra: map[string]any{
		OpenAIFirstTokenMetricModeExtraKey: OpenAIFirstTokenMetricModeFirstOutput,
	}}
	require.Equal(t, OpenAIFirstTokenMetricModeFirstOutput, account.OpenAIFirstTokenMetricMode())
	require.False(t, account.UseOpenAIFirstResponseTTFT())

	account.Extra[OpenAIFirstTokenMetricModeExtraKey] = "invalid"
	require.Equal(t, OpenAIFirstTokenMetricModeFirstResponse, account.OpenAIFirstTokenMetricMode())
	require.True(t, account.UseOpenAIFirstResponseTTFT())
}
