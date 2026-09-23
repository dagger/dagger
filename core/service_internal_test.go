package core

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/executor"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/require"
)

func TestConvertResizeChannel(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, convertResizeChannel(t.Context(), nil))
	})

	for _, mode := range []string{"cancel blocked send", "closed input", "forward resize"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				in := make(chan bkgw.WinSize, 1)
				if mode == "closed input" {
					close(in)
				} else {
					in <- bkgw.WinSize{Rows: 24, Cols: 80}
				}
				out := convertResizeChannel(ctx, in)
				t.Cleanup(func() {
					cancel()
					// Release a broken forwarder's pending send even after a
					// failed assertion, then observe its deferred close.
					timer := time.NewTimer(5 * time.Second)
					defer timer.Stop()
					for {
						select {
						case _, open := <-out:
							if !open {
								return
							}
						case <-timer.C:
							t.Error("resize forwarder did not exit during cleanup")
							return
						}
					}
				})

				// With no output reader, the pending resize must reach its
				// blocked send before cancellation is tested.
				synctest.Wait()
				if mode == "forward resize" {
					select {
					case size, open := <-out:
						require.True(t, open)
						require.Equal(t, executor.WinSize{Rows: 24, Cols: 80}, size)
					case <-time.After(5 * time.Second):
						t.Fatal("resize forwarder did not deliver the resize")
					}
				}
				if mode != "closed input" {
					cancel()
				}
				synctest.Wait()
				select {
				case _, open := <-out:
					require.False(t, open, "resize forwarder must exit and close its output")
				default:
					t.Fatal("resize forwarder did not close its output")
				}
			})
		})
	}
}

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
