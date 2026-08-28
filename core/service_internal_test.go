package core

import (
	"testing"

	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

func TestRecordBoundServiceFQDNs(t *testing.T) {
	t.Parallel()

	const daggerFQDN = "svc-host.abc123.def456.dagger.local"

	t.Run("records the name the running service registered under", func(t *testing.T) {
		t.Parallel()

		execMD := &engineutil.ExecutionMetadata{}
		recordBoundServiceFQDNs(execMD,
			ServiceBindings{{Hostname: "svc-host", Aliases: AliasSet{"svc-host"}}},
			[]*RunningService{{Host: daggerFQDN}},
		)
		require.Equal(t, map[string]string{"svc-host": daggerFQDN}, execMD.HostAliasFQDNs)
	})

	t.Run("skips hosts outside the engine's DNS domain", func(t *testing.T) {
		t.Parallel()

		// A tunnel service reports a host-side dial address, which would be
		// meaningless written into the container's hosts file.
		execMD := &engineutil.ExecutionMetadata{}
		recordBoundServiceFQDNs(execMD,
			ServiceBindings{{Hostname: "tunnel-host"}},
			[]*RunningService{{Host: "127.0.0.1"}},
		)
		require.Nil(t, execMD.HostAliasFQDNs)
	})

	t.Run("skips a host already equal to the bound hostname", func(t *testing.T) {
		t.Parallel()

		execMD := &engineutil.ExecutionMetadata{}
		recordBoundServiceFQDNs(execMD,
			ServiceBindings{{Hostname: daggerFQDN}},
			[]*RunningService{{Host: daggerFQDN}},
		)
		require.Nil(t, execMD.HostAliasFQDNs)
	})

	t.Run("skips unstarted and missing entries", func(t *testing.T) {
		t.Parallel()

		execMD := &engineutil.ExecutionMetadata{}
		recordBoundServiceFQDNs(execMD,
			ServiceBindings{
				{Hostname: "nil-svc"},
				{Hostname: "empty-host"},
				{Hostname: "past-the-end"},
			},
			[]*RunningService{nil, {Host: ""}},
		)
		require.Nil(t, execMD.HostAliasFQDNs)
	})

	t.Run("does not write through a map shared with a shallow clone", func(t *testing.T) {
		t.Parallel()

		shared := map[string]string{"pre-existing": "other.dagger.local"}
		execMD := &engineutil.ExecutionMetadata{HostAliasFQDNs: shared}
		recordBoundServiceFQDNs(execMD,
			ServiceBindings{{Hostname: "svc-host"}},
			[]*RunningService{{Host: daggerFQDN}},
		)
		require.Equal(t, map[string]string{"svc-host": daggerFQDN}, execMD.HostAliasFQDNs)
		require.Equal(t, map[string]string{"pre-existing": "other.dagger.local"}, shared)
	})

	t.Run("tolerates absent metadata", func(t *testing.T) {
		t.Parallel()

		require.NotPanics(t, func() {
			recordBoundServiceFQDNs(nil,
				ServiceBindings{{Hostname: "svc-host"}},
				[]*RunningService{{Host: daggerFQDN}},
			)
		})
	})
}
