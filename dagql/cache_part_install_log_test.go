package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// A part installed from an offer is logged once, at info level, with the
// entry, the part, the bytes downloaded, the time taken and the source. The
// bytes are those read from the offer's addresses: every layer when the
// cache has none of the chain, none when it already has it all.
func TestOfferInstallLogged(t *testing.T) {
	for _, tc := range []struct {
		name string
		// held imports the chain into the cache's store first.
		held bool
	}{
		{name: "downloaded"},
		{name: "already held", held: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := newChainFixture(t, "install-log")
			f := newExhaustionFixture(t, chain)
			f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
			var size int64
			for _, layer := range chain.chain.Layers {
				size += layer.Descriptor.Size
			}
			require.Positive(t, size)
			want := size
			if tc.held {
				held, err := f.cache.snapshotManager.ImportChain(f.ctx, &snapshots.ExportChain{Layers: chain.chain.Layers, Provider: chain.chain.Provider})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, held.Release(context.Background())) })
				want = 0
			}
			var logs bytes.Buffer
			f.ctx = slog.WithLogger(f.ctx, slog.New(slog.NewJSONHandler(&logs, nil)))

			require.NoError(t, f.run())
			require.True(t, f.installed(), "the part installed from its offer")

			var installs []map[string]any
			dec := json.NewDecoder(&logs)
			dec.UseNumber()
			for dec.More() {
				var line map[string]any
				require.NoError(t, dec.Decode(&line))
				if line["msg"] == "remote cache part installed" {
					installs = append(installs, line)
				}
			}
			require.Len(t, installs, 1)
			line := installs[0]
			require.Equal(t, "INFO", line["level"])
			require.Equal(t, json.Number(strconv.FormatUint(uint64(f.receiver.cacheSharedResult().id), 10)), line["entry"])
			require.Equal(t, "", line["output"])
			require.Equal(t, "snapshot", line["part"])
			require.Equal(t, json.Number(strconv.FormatInt(want, 10)), line["bytes"])
			require.Equal(t, "offer", line["source"])
			duration, err := line["duration"].(json.Number).Int64()
			require.NoError(t, err)
			require.Positive(t, duration)
		})
	}
}

// A part installed from an offer is also recorded, once, on the
// lazy-evaluation span of the task that installed it, which names the
// receiving entry: the part, the bytes downloaded and the time taken. Here
// that is the lazy-operation decision's task, which demandPart runs when no
// source was selected before it. When the producing call's span was captured,
// the span is the resume span, not the producer's ended span.
func TestOfferInstallOnItsSpan(t *testing.T) {
	for _, resume := range []bool{false, true} {
		name := "lazy op"
		if resume {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			chain := newChainFixture(t, "install-span")
			f := newExhaustionFixture(t, chain)
			f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
			var size int64
			for _, layer := range chain.chain.Layers {
				size += layer.Descriptor.Size
			}
			require.Positive(t, size)
			sr, _, root := newLazyRecordingRoot("POST /query")
			f.ctx = trace.ContextWithSpan(f.ctx, root)
			var producer trace.Span
			if resume {
				var producerCtx context.Context
				producerCtx, producer = Tracer(f.ctx).Start(f.ctx, "Query.receiver")
				producer.End()
				f.cache.captureSessionLazySpanContext(producerCtx, cacheTestSessionID(t, f.ctx), f.receiver)
			}

			require.NoError(t, f.run())
			require.True(t, f.installed(), "the part installed from its offer")
			root.End()

			var stamped []sdktrace.ReadOnlySpan
			for _, s := range sr.Ended() {
				for _, kv := range s.Attributes() {
					if string(kv.Key) == telemetryattrs.CacheOfferPartAttr {
						stamped = append(stamped, s)
					}
				}
			}
			require.Len(t, stamped, 1, "one span records the install")
			span := stamped[0]
			attrs := map[string]attribute.Value{}
			for _, kv := range span.Attributes() {
				attrs[string(kv.Key)] = kv.Value
			}
			key, err := partAddressKey(f.address)
			require.NoError(t, err)
			require.Equal(t, key, attrs[telemetryattrs.CacheOfferPartAttr].AsString())
			require.Equal(t, strconv.FormatInt(size, 10), attrs[telemetryattrs.CacheOfferBytesAttr].AsString())
			duration, err := strconv.ParseInt(attrs[telemetryattrs.CacheOfferDurationAttr].AsString(), 10, 64)
			require.NoError(t, err)
			require.Positive(t, duration)
			require.Equal(t, strconv.FormatUint(uint64(f.receiver.cacheSharedResult().id), 10), attrs[telemetryattrs.CacheResultIDAttr].AsString(), "the span names the receiving entry")
			require.Equal(t, wcprof.OpKindLazy.String(), attrs[telemetryattrs.WcprofOpKindAttr].AsString(), "a lazy-evaluation span")
			if resume {
				require.NotEqual(t, producer.SpanContext().SpanID(), span.SpanContext().SpanID())
				require.True(t, strings.HasPrefix(span.Name(), "resume "), "got %q", span.Name())
			}
		})
	}
}
