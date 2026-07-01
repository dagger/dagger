//go:build linux

package oci

import (
	"context"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/executor"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func minimalSpec() *specs.Spec {
	return &specs.Spec{
		Linux: &specs.Linux{},
	}
}

func TestGenerateResourceOptsZeroIsNoop(t *testing.T) {
	t.Parallel()
	opts := generateResourceOpts(executor.Meta{})
	require.Nil(t, opts)
}

func TestGenerateResourceOptsMemoryLimit(t *testing.T) {
	t.Parallel()
	opts := generateResourceOpts(executor.Meta{MemoryBytes: 512 * 1024 * 1024})
	require.Len(t, opts, 1)

	s := minimalSpec()
	require.NoError(t, opts[0](context.Background(), nil, nil, s))
	require.NotNil(t, s.Linux.Resources.Memory.Limit)
	require.Equal(t, int64(512*1024*1024), *s.Linux.Resources.Memory.Limit)
	require.Nil(t, s.Linux.Resources.Memory.Reservation)
}

func TestGenerateResourceOptsMemorySoft(t *testing.T) {
	t.Parallel()
	opts := generateResourceOpts(executor.Meta{MemorySoftBytes: 256 * 1024 * 1024})
	require.Len(t, opts, 1)

	s := minimalSpec()
	require.NoError(t, opts[0](context.Background(), nil, nil, s))
	require.Nil(t, s.Linux.Resources.Memory.Limit)
	require.NotNil(t, s.Linux.Resources.Memory.Reservation)
	require.Equal(t, int64(256*1024*1024), *s.Linux.Resources.Memory.Reservation)
}

func TestGenerateResourceOptsCPUQuota(t *testing.T) {
	t.Parallel()
	opts := generateResourceOpts(executor.Meta{CPUQuota: 150000, CPUPeriod: 100000})
	require.Len(t, opts, 1)

	s := minimalSpec()
	require.NoError(t, opts[0](context.Background(), nil, nil, s))
	require.NotNil(t, s.Linux.Resources.CPU.Quota)
	require.Equal(t, int64(150000), *s.Linux.Resources.CPU.Quota)
	require.NotNil(t, s.Linux.Resources.CPU.Period)
	require.Equal(t, uint64(100000), *s.Linux.Resources.CPU.Period)
}

func TestGenerateResourceOptsCPUQuotaDefaultPeriod(t *testing.T) {
	t.Parallel()
	// CPUPeriod == 0 should default to 100000
	opts := generateResourceOpts(executor.Meta{CPUQuota: 150000})
	require.Len(t, opts, 1)

	s := minimalSpec()
	require.NoError(t, opts[0](context.Background(), nil, nil, s))
	require.Equal(t, uint64(100000), *s.Linux.Resources.CPU.Period)
}

func TestGenerateResourceOptsCPUShares(t *testing.T) {
	t.Parallel()
	opts := generateResourceOpts(executor.Meta{CPUShares: 512})
	require.Len(t, opts, 1)

	s := minimalSpec()
	require.NoError(t, opts[0](context.Background(), nil, nil, s))
	require.NotNil(t, s.Linux.Resources.CPU.Shares)
	require.Equal(t, uint64(512), *s.Linux.Resources.CPU.Shares)
}

func TestGenerateResourceOptsPids(t *testing.T) {
	t.Parallel()
	opts := generateResourceOpts(executor.Meta{PidsLimit: 256})
	require.Len(t, opts, 1)

	s := minimalSpec()
	require.NoError(t, opts[0](context.Background(), nil, nil, s))
	require.NotNil(t, s.Linux.Resources.Pids.Limit)
	require.Equal(t, int64(256), *s.Linux.Resources.Pids.Limit)
}
