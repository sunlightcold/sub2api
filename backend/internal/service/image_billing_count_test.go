package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveBillableImageCount_DefaultKeepsOutputCount(t *testing.T) {
	require.Equal(t, 2, resolveBillableImageCount(&Group{}, 2, 1))
	require.Equal(t, 2, resolveBillableImageCount(&Group{ImageBillingUseRequestedCount: boolPtr(false)}, 2, 1))
	require.Equal(t, 2, resolveBillableImageCount(nil, 2, 1))
}

func TestResolveBillableImageCount_EnabledUsesRequestedCount(t *testing.T) {
	require.Equal(t, 1, resolveBillableImageCount(&Group{ImageBillingUseRequestedCount: boolPtr(true)}, 2, 1))
	require.Equal(t, 2, resolveBillableImageCount(&Group{ImageBillingUseRequestedCount: boolPtr(true)}, 2, 0))
	require.Equal(t, 0, resolveBillableImageCount(&Group{ImageBillingUseRequestedCount: boolPtr(true)}, 0, 1))
}
