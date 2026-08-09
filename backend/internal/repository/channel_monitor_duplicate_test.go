//go:build unit

package repository

import (
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorDuplicateOperationMetadataStaysOutOfRuntimeHeaders(t *testing.T) {
	monitor := &service.ChannelMonitor{
		ExtraHeaders:         map[string]string{"User-Agent": "Codex"},
		DuplicateOperationID: "operation-digest",
		PassLatencyMinMs:     1500,
		PassLatencyMaxMs:     14500,
	}

	persisted := channelMonitorHeadersForPersistence(monitor)
	require.Equal(t, "operation-digest", persisted[service.ChannelMonitorDuplicateOperationIDMetadataKey])
	require.Equal(t, "Codex", persisted["User-Agent"])
	require.Equal(t, "1500", persisted[service.ChannelMonitorPassLatencyMinMsMetadataKey])
	require.Equal(t, "14500", persisted[service.ChannelMonitorPassLatencyMaxMsMetadataKey])
	require.NotContains(t, monitor.ExtraHeaders, service.ChannelMonitorDuplicateOperationIDMetadataKey)

	restored := entToServiceMonitor(&dbent.ChannelMonitor{ExtraHeaders: persisted})
	require.Equal(t, "operation-digest", restored.DuplicateOperationID)
	require.Equal(t, map[string]string{"User-Agent": "Codex"}, restored.ExtraHeaders)
	require.Equal(t, 1500, restored.PassLatencyMinMs)
	require.Equal(t, 14500, restored.PassLatencyMaxMs)
	require.NotContains(t, restored.ExtraHeaders, service.ChannelMonitorDuplicateOperationIDMetadataKey)
	require.Equal(t, "operation-digest", persisted[service.ChannelMonitorDuplicateOperationIDMetadataKey], "decoding must not mutate the ent row")
}

func TestChannelMonitorPassLatencyMetadataUsesDefaultsWhenMissing(t *testing.T) {
	restored := entToServiceMonitor(&dbent.ChannelMonitor{ExtraHeaders: map[string]string{}})
	require.Equal(t, service.MonitorDefaultPassLatencyMinMs, restored.PassLatencyMinMs)
	require.Equal(t, service.MonitorDefaultPassLatencyMaxMs, restored.PassLatencyMaxMs)
}
