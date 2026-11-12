package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// offerLogs captures the JSON lines logged through a context's logger.
type offerLogs struct {
	out lockedBuffer
}

func (l *offerLogs) into(ctx context.Context) context.Context {
	return slog.WithLogger(ctx, slog.New(slog.NewJSONHandler(&l.out, nil)))
}

// offerLine is one logged line: its raw text and its decoded fields.
type offerLine struct {
	raw    string
	fields map[string]any
}

func (l *offerLogs) lines(t *testing.T, msg string) []offerLine {
	t.Helper()
	l.out.mu.Lock()
	text := l.out.buf.String()
	l.out.mu.Unlock()
	var lines []offerLine
	for raw := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		if raw == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		var fields map[string]any
		require.NoError(t, dec.Decode(&fields))
		if fields["msg"] == msg {
			lines = append(lines, offerLine{raw: raw, fields: fields})
		}
	}
	return lines
}

func (l offerLine) int(t *testing.T, key string) int64 {
	t.Helper()
	n, ok := l.fields[key].(json.Number)
	require.True(t, ok, "%s is a JSON number: %v", key, l.fields[key])
	v, err := n.Int64()
	require.NoError(t, err)
	return v
}

func (l offerLine) duration(t *testing.T, key string) time.Duration {
	t.Helper()
	return time.Duration(l.int(t, key))
}

// offerTimingFields are the timing fields of both install lines, in order,
// after duration; commit follows them.
var offerTimingFields = []string{"layers", "requests", "ttfb", "conns_new", "renew", "read_wait", "layer_wait", "fetch", "apply", "import"}

// requireOfferTiming checks the identities every install line's timing
// holds: the request wait is part of the read wait, the import's three sums
// of the import, and the import and commit of the whole install. The read
// wait sums the layers' reads, which run at once, so it can exceed the
// import.
func requireOfferTiming(t *testing.T, line offerLine) {
	t.Helper()
	for _, key := range append([]string{"waited", "duration", "commit"}, offerTimingFields...) {
		require.GreaterOrEqual(t, line.int(t, key), int64(0), key)
	}
	require.LessOrEqual(t, line.duration(t, "ttfb"), line.duration(t, "read_wait"))
	require.LessOrEqual(t, line.duration(t, "layer_wait")+line.duration(t, "fetch")+line.duration(t, "apply"), line.duration(t, "import"))
	require.LessOrEqual(t, line.duration(t, "import")+line.duration(t, "commit"), line.duration(t, "duration"))
}

// A part installed from an offer is logged once, at info level, with the
// entry, the part, the bytes downloaded, the time taken and the source, and
// then where the time went. The bytes are those read from the offer's
// addresses: every layer when the cache has none of the chain, the upper
// layer when it holds the lower one, none when it already has it all. A
// reused layer costs no request, fetch or apply; the import still runs.
func TestOfferInstallLogged(t *testing.T) {
	for _, tc := range []struct {
		name string
		// held imports the first held layers of the chain into the cache's
		// store first.
		held int
		// demanded starts the demand this long before the install.
		demanded time.Duration
	}{
		{name: "downloaded", demanded: 50 * time.Millisecond},
		{name: "lower held", held: 1, demanded: 50 * time.Millisecond},
		{name: "already held", held: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := newChainFixture(t, "install-log")
			f := newExhaustionFixture(t, chain)
			f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
			layers := chain.chain.Layers
			var want int64
			for _, layer := range layers[tc.held:] {
				want += layer.Descriptor.Size
			}
			if tc.held > 0 {
				held, err := f.cache.snapshotManager.ImportChain(f.ctx, &snapshots.ExportChain{Layers: layers[:tc.held], Provider: chain.chain.Provider})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, held.Release(context.Background())) })
			}
			logs := f.logs
			if tc.demanded > 0 {
				f.demand.started = time.Now().Add(-tc.demanded)
			}

			require.NoError(t, f.run())
			ran := time.Now()
			require.True(t, f.installed(), "the part installed from its offer")

			installs := logs.lines(t, "remote cache part installed")
			require.Len(t, installs, 1)
			require.Empty(t, logs.lines(t, "remote cache part install failed"))
			line := installs[0]
			require.Equal(t, "INFO", line.fields["level"])
			require.Equal(t, json.Number(strconv.FormatUint(uint64(f.receiver.cacheSharedResult().id), 10)), line.fields["entry"])
			require.Equal(t, "", line.fields["output"])
			require.Equal(t, "snapshot", line.fields["part"])
			require.Equal(t, json.Number(strconv.FormatInt(want, 10)), line.fields["bytes"])
			require.Equal(t, "offer", line.fields["source"])
			duration := line.duration(t, "duration")
			require.Positive(t, duration)
			requireOfferTiming(t, line)
			require.EqualValues(t, len(layers), line.int(t, "layers"))
			require.EqualValues(t, f.transport.total(), line.int(t, "requests"))
			require.EqualValues(t, len(layers)-tc.held, line.int(t, "requests"), "one request per downloaded layer")
			require.Positive(t, line.duration(t, "import"), "the import runs, reused layers or not")
			require.Zero(t, line.int(t, "conns_new"), "the in-process transport opens no connections")
			require.Zero(t, line.int(t, "renew"), "fixed addresses need no renewal")
			if tc.held < len(layers) {
				require.Positive(t, line.duration(t, "read_wait"))
				require.Positive(t, line.duration(t, "fetch"))
				require.Positive(t, line.duration(t, "apply"))
			} else {
				for _, key := range []string{"bytes", "requests", "ttfb", "read_wait", "fetch", "apply"} {
					require.Zero(t, line.int(t, key), key)
				}
			}
			waited := line.duration(t, "waited")
			if tc.demanded > 0 {
				require.GreaterOrEqual(t, waited, tc.demanded)
				require.LessOrEqual(t, waited+duration, ran.Sub(f.demand.started), "within the demand's observed time")
			} else {
				require.Zero(t, waited, "a demand with no start reports no wait")
			}
			// The original fields keep their order, and the new ones follow.
			keys := append([]string{"entry", "output", "part", "bytes", "duration", "source", "waited"}, offerTimingFields...)
			requireFieldOrder(t, line.raw, append(keys, "commit"))
		})
	}
}

// Over a real keep-alive transport, an install's layers download at once,
// so a cold transport opens one connection per layer, and a later install
// reuses them.
func TestOfferInstallLoggedConnections(t *testing.T) {
	chain := newChainFixture(t, "install-conns")
	// The cold install's requests are answered once all are in flight.
	all := make(chan struct{})
	var arrived atomic.Int64
	var warm atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil || i < 0 || i >= len(chain.blobs) {
			http.NotFound(w, r)
			return
		}
		if !warm.Load() {
			if arrived.Add(1) == int64(len(chain.blobs)) {
				close(all)
			}
			select {
			case <-all:
			case <-time.After(30 * time.Second):
				t.Errorf("the cold install's requests were not all in flight at once")
			}
		}
		http.ServeContent(w, r, "blob", time.Time{}, bytes.NewReader(chain.blobs[i]))
	}))
	defer server.Close()
	transport := server.Client().Transport
	install := func() offerLine {
		f := newExhaustionFixture(t, chain)
		f.cache.partContentSource = NewPartContentSource(transport)
		offer := exhaustionOffer(chain, "", true)
		for i := range chain.blobs {
			offer.Chain.Addresses[chain.digest(i)] = BlobAddress{URL: fmt.Sprintf("%s/%d", server.URL, i)}
		}
		f.attach(t, f.receiver, offer)

		require.NoError(t, f.run())
		require.True(t, f.installed())
		installs := f.logs.lines(t, "remote cache part installed")
		require.Len(t, installs, 1)
		require.EqualValues(t, len(chain.blobs), installs[0].int(t, "requests"))
		requireOfferTiming(t, installs[0])
		return installs[0]
	}
	cold := install()
	require.EqualValues(t, len(chain.blobs), cold.int(t, "conns_new"), "one connection per layer")
	warm.Store(true)
	again := install()
	require.Zero(t, again.int(t, "conns_new"), "a later install reuses the connections")
}

// requireFieldOrder checks that keys appear in raw, a JSON line, in order.
func requireFieldOrder(t *testing.T, raw string, keys []string) {
	t.Helper()
	at := 0
	for _, key := range keys {
		i := strings.Index(raw[at:], `"`+key+`":`)
		require.GreaterOrEqual(t, i, 0, "%s after the fields before it in %s", key, raw)
		at += i + len(key) + 3
	}
}

// A part installed from an offer is also recorded, once, on the
// lazy-evaluation span of the task that installed it, which names the
// receiving entry: the part, the bytes downloaded and the time taken. Here
// that is the lazy-operation decision's task, which demandPart runs when no
// source was selected before it. When the producing call's span was captured,
// the span is the resume span, not the producer's ended span. The timing
// attributes carry the logged values.
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
			logs := f.logs
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
			installs := logs.lines(t, "remote cache part installed")
			require.Len(t, installs, 1)
			for attr, field := range map[string]string{
				telemetryattrs.CacheOfferDurationAttr:  "duration",
				telemetryattrs.CacheOfferWaitedAttr:    "waited",
				telemetryattrs.CacheOfferLayersAttr:    "layers",
				telemetryattrs.CacheOfferRequestsAttr:  "requests",
				telemetryattrs.CacheOfferTTFBAttr:      "ttfb",
				telemetryattrs.CacheOfferConnsNewAttr:  "conns_new",
				telemetryattrs.CacheOfferRenewAttr:     "renew",
				telemetryattrs.CacheOfferReadWaitAttr:  "read_wait",
				telemetryattrs.CacheOfferLayerWaitAttr: "layer_wait",
				telemetryattrs.CacheOfferFetchAttr:     "fetch",
				telemetryattrs.CacheOfferApplyAttr:     "apply",
				telemetryattrs.CacheOfferImportAttr:    "import",
				telemetryattrs.CacheOfferCommitAttr:    "commit",
			} {
				value, ok := attrs[attr]
				require.True(t, ok, "%s is on the span", attr)
				n, err := strconv.ParseInt(value.AsString(), 10, 64)
				require.NoError(t, err, attr)
				require.Equal(t, installs[0].int(t, field), n, "%s is the logged %s", attr, field)
			}
			if resume {
				require.NotEqual(t, producer.SpanContext().SpanID(), span.SpanContext().SpanID())
				require.True(t, strings.HasPrefix(span.Name(), "resume "), "got %q", span.Name())
			}
		})
	}
}

// An install that reaches the import and installs nothing logs one failed
// line, and stamps no span. Bad content exhausts the source, at warn level,
// with the bytes it cost and no address; cancellation is at info level.
func TestOfferInstallFailedLogged(t *testing.T) {
	t.Run("content", func(t *testing.T) {
		chain := newChainFixture(t, "install-corrupt")
		f := newExhaustionFixture(t, chain)
		f.transport.fault("https://fixed.invalid/install-corrupt/1", contentTestFault{corrupt: true, stallAfter: -1})
		f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
		sr, _, root := newLazyRecordingRoot("POST /query")
		f.ctx = trace.ContextWithSpan(f.ctx, root)
		logs := f.logs

		require.ErrorIs(t, f.run(), errRenewalFallbackRan, "the exhausted source falls back to the operation")
		root.End()

		require.Empty(t, logs.lines(t, "remote cache part installed"))
		failed := logs.lines(t, "remote cache part install failed")
		require.Len(t, failed, 1)
		line := failed[0]
		require.Equal(t, "WARN", line.fields["level"])
		require.Equal(t, "content", line.fields["outcome"])
		require.Equal(t, json.Number(strconv.FormatUint(uint64(f.receiver.cacheSharedResult().id), 10)), line.fields["entry"])
		require.Equal(t, "snapshot", line.fields["part"])
		require.Positive(t, line.int(t, "bytes"))
		require.GreaterOrEqual(t, line.int(t, "requests"), int64(1))
		require.EqualValues(t, f.transport.total(), line.int(t, "requests"))
		require.EqualValues(t, len(chain.chain.Layers), line.int(t, "layers"))
		require.NotContains(t, line.fields, "commit", "the import did not return")
		require.NotEmpty(t, line.fields["error"])
		require.NotContains(t, line.raw, "https://fixed.invalid")
		keys := append([]string{"entry", "output", "part", "outcome", "bytes", "waited", "duration"}, offerTimingFields...)
		requireFieldOrder(t, line.raw, append(keys, "error"))
		for _, s := range sr.Ended() {
			for _, kv := range s.Attributes() {
				require.NotEqual(t, telemetryattrs.CacheOfferPartAttr, string(kv.Key), "span %q", s.Name())
			}
		}
	})
	t.Run("canceled", func(t *testing.T) {
		chain := newChainFixture(t, "install-canceled")
		f := newExhaustionFixture(t, chain)
		f.transport.opened = make(chan struct{}, 4)
		f.transport.fault("https://fixed.invalid/install-canceled/0", contentTestFault{stallAfter: 0})
		f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
		logs := f.logs
		ctx, cancel := context.WithCancel(f.ctx)
		defer cancel()
		f.ctx = ctx
		demanded := make(chan error, 1)
		go func() { demanded <- f.run() }()
		within(t, f.transport.opened)
		cancel()
		require.ErrorIs(t, within(t, demanded), context.Canceled)
		var failed []offerLine
		require.Eventually(t, func() bool {
			failed = logs.lines(t, "remote cache part install failed")
			return len(failed) > 0
		}, 10*time.Second, 10*time.Millisecond)
		require.Len(t, failed, 1)
		require.Equal(t, "INFO", failed[0].fields["level"])
		require.Equal(t, "canceled", failed[0].fields["outcome"])
		// The upper layer downloads while the lower one's body stalls; the
		// stalled body gave nothing.
		require.GreaterOrEqual(t, failed[0].int(t, "requests"), int64(1))
		require.LessOrEqual(t, failed[0].int(t, "requests"), int64(len(chain.blobs)))
		require.LessOrEqual(t, failed[0].int(t, "bytes"), int64(len(chain.blobs[1])), "at most the upper layer's bytes")
		require.Empty(t, logs.lines(t, "remote cache part installed"))
	})
}
