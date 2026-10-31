package dagql

// Cache-evidence facts about remote candidates and new inputs: which inputs of
// a missed call were new to the cache, which lost candidates were values a
// Cloud merge installed, publication adopting a merged entry, and the merge
// origin a lazy-evaluation span reports.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// evidenceRemoteTestRootFrame is the frame mergeTestSource's root records.
func evidenceRemoteTestRootFrame() *ResultCall {
	return &ResultCall{Kind: ResultCallKindField, Field: "root", Type: NewResultCallType((&transferTestValue{}).Type())}
}

// evidenceRemoteTestWitnessed is field over one structural input: a digest
// witness of dig.
func evidenceRemoteTestWitnessed(field string, dig digest.Digest) *ResultCall {
	frame := cacheTestIntCall(field)
	frame.Args = []*ResultCallArg{{
		Name: "src",
		Value: &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  "witnessed",
			DigestedStringDigest: dig,
		},
	}}
	return frame
}

// A miss names the inputs whose class no term uses yet: a known digest that
// no call has taken as an input is new to the cache. Once a call over it is
// published, another operation over it doesn't count it.
func TestCacheEvidenceFreshInputs(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)
	run := func(t *testing.T, frame *ResultCall) *CacheDecision {
		t.Helper()
		req := cacheTestArmedRequest(frame)
		res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(frame, 1), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !res.HitCache())
		assert.Equal(t, CacheOutcomeExecuted, req.CacheEvidence.Outcome)
		return req.CacheEvidence
	}

	// The digest gets a class, as a result's extra digest, but no term over it.
	input := digest.FromString("evidence-fresh-input")
	teacher := cacheTestIntCall("evidence-fresh-teacher", call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: input})
	_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: teacher}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(teacher, 0), nil
	})
	assert.NilError(t, err)

	ev := run(t, evidenceRemoteTestWitnessed("evidence-fresh-op", input))
	assert.Equal(t, -1, ev.MissUnknownInputIndex, "the input is known")
	assert.DeepEqual(t, []int{0}, ev.MissFreshInputs)
	assert.Assert(t, !ev.Adopted, "a fresh publication adopts nothing")

	ev = run(t, evidenceRemoteTestWitnessed("evidence-fresh-other-op", input))
	assert.Equal(t, 0, len(ev.MissFreshInputs), "a published call uses the input now")

	// An unknown input makes the structural lookup impossible, and is not
	// reported as fresh.
	ev = run(t, evidenceRemoteTestWitnessed("evidence-fresh-op", digest.FromString("evidence-fresh-unknown")))
	assert.Equal(t, 0, ev.MissUnknownInputIndex)
	assert.Equal(t, 0, len(ev.MissFreshInputs))
}

// Expiry that eliminates a merged value says the lost candidate was remote; a
// local expired value doesn't.
func TestCacheEvidenceExpiredRemote(t *testing.T) {
	t.Parallel()
	miss := func(t *testing.T, ctx context.Context, c *Cache, srv *Server) *CacheDecision {
		t.Helper()
		frame := evidenceRemoteTestRootFrame()
		req := &CallRequest{ResultCall: frame, IsPersistable: true, CacheEvidence: NewCacheDecision()}
		res, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "computed"}, frame)
		})
		assert.NilError(t, err)
		assert.Assert(t, !res.HitCache())
		assert.Equal(t, CacheOutcomeExecuted, req.CacheEvidence.Outcome)
		return req.CacheEvidence
	}

	t.Run("merged", func(t *testing.T) {
		t.Parallel()
		bundle, _ := mergeTestSource(t, "root", "sent")
		ctx, c, srv := transferTestCache(t)
		reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
		assert.NilError(t, err)
		merged, err := c.LoadResultByResultID(ctx, "", srv, reply.Imported()[0].ResultID)
		assert.NilError(t, err)
		currentEntryTestExpire(c, merged)
		ev := miss(t, ctx, c, srv)
		assert.Assert(t, ev.MissSawExpired)
		assert.Assert(t, ev.MissExpiredRemote)
	})

	t.Run("local", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := transferTestCache(t)
		local := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "local"})
		currentEntryTestExpire(c, local)
		ev := miss(t, ctx, c, srv)
		assert.Assert(t, ev.MissSawExpired)
		assert.Assert(t, !ev.MissExpiredRemote)
	})
}

// A session-incompatible candidate that is a merged value says so.
func TestCacheEvidenceIncompatibleRemote(t *testing.T) {
	t.Parallel()
	for _, remote := range []bool{false, true} {
		name := "local"
		if remote {
			name = "remote"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			baseCtx := cacheTestContext(t.Context())
			c, err := NewCache(baseCtx, "", nil, nil)
			assert.NilError(t, err)
			ctxA := ContextWithCache(baseCtx, c)
			handle := SessionResourceHandle("evidence-incompatible-remote-secret")
			assert.NilError(t, c.BindSessionResource(ctxA, "test-session", "dagql-test-client", handle, "concrete"))
			frame := cacheTestIntCall("evidence-incompatible-remote")
			first, err := c.GetOrInitCall(ctxA, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
				return cacheTestIntResult(frame, 1).(Result[Int]).WithSessionResourceHandleAny(ctxA, handle)
			})
			assert.NilError(t, err)
			if remote {
				// As a merge leaves it (installMergedRecordLocked).
				c.egraphMu.Lock()
				first.cacheSharedResult().imported = true
				c.egraphMu.Unlock()
			}

			ctxB := ContextWithCache(engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
				ClientID:  "dagql-test-client-b",
				SessionID: "test-session-b",
			}), c)
			req := cacheTestArmedRequest(frame)
			res, err := c.GetOrInitCall(ctxB, "test-session-b", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
				return cacheTestIntResult(frame, 2).(Result[Int]).WithSessionResourceHandleAny(ctxB, handle)
			})
			assert.NilError(t, err)
			assert.Assert(t, !res.HitCache())
			ev := req.CacheEvidence
			assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
			assert.Assert(t, ev.MissIncompatibleCandidates)
			assert.Equal(t, remote, ev.MissIncompatibleRemote)
		})
	}
}

// A merge that lands between a call's lookup and its publication is adopted
// by the publication, and the call's span says so, with the merge's commit
// time after the lookup.
func TestCacheEvidenceAdoptedRemote(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	ctx, c, srv := transferTestCache(t)
	var (
		once     sync.Once
		reply    MergeReply
		mergeErr error
	)
	c.testBeforePublicationIndex = func(*ongoingCall) {
		once.Do(func() { reply, mergeErr = c.MergeValues(ctx, cloudCacheID, bundle) })
	}
	frame := evidenceRemoteTestRootFrame()
	req := &CallRequest{ResultCall: frame, IsPersistable: true, CacheEvidence: NewCacheDecision()}
	res, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "computed"}, frame)
	})
	assert.NilError(t, err)
	assert.NilError(t, mergeErr)
	assert.Equal(t, reply.Imported()[0].ResultID, uint64(res.cacheSharedResult().id), "the publication adopted the merged entry")

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
	assert.Assert(t, ev.Adopted)
	assert.Assert(t, ev.AdoptedRemote)
	assert.Assert(t, ev.AdoptedMergedAtUnixNano > ev.LookupAtUnixNano,
		"merged at %d, looked up at %d", ev.AdoptedMergedAtUnixNano, ev.LookupAtUnixNano)
}

// A publication that adopts another session's live entry says so, and the
// entry isn't remote.
func TestCacheEvidenceAdoptedLocal(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, c)
	frame := cacheTestIntCall("evidence-adopted-local")
	detached := cacheTestIntResult(frame, 1)
	parked, resume := make(chan struct{}), make(chan struct{})
	c.testBeforePublicationIndex = func(oc *ongoingCall) {
		if oc.val != nil && oc.val.cacheSharedResult() == detached.cacheSharedResult() {
			close(parked)
			<-resume
		}
	}
	type outcome struct {
		res AnyResult
		err error
	}
	req := cacheTestArmedRequest(frame)
	done := make(chan outcome, 1)
	go func() {
		res, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
			return detached, nil
		})
		done <- outcome{res, err}
	}()
	select {
	case <-parked:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the publication to park")
	}
	winner, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 2), nil
	})
	assert.NilError(t, err)
	close(resume)
	a := <-done
	assert.NilError(t, a.err)
	assert.Equal(t, winner.cacheSharedResult().id, a.res.cacheSharedResult().id, "the publication adopted session B's entry")

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
	assert.Assert(t, ev.Adopted)
	assert.Assert(t, !ev.AdoptedRemote)
	assert.Equal(t, int64(0), ev.AdoptedMergedAtUnixNano)
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
}

// A lazy-evaluation span's state names a merged value's origin, and a local
// value's not.
func TestLazySpanCacheStateRemote(t *testing.T) {
	t.Parallel()
	t.Run("merged", func(t *testing.T) {
		t.Parallel()
		bundle, _ := mergeTestSource(t, "root", "sent")
		ctx, c, srv := transferTestCache(t)
		before := time.Now().UnixNano()
		reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
		assert.NilError(t, err)
		after := time.Now().UnixNano()
		merged, err := c.LoadResultByResultID(ctx, "", srv, reply.Imported()[0].ResultID)
		assert.NilError(t, err)
		state := c.lazySpanCacheState(ctx, merged.cacheSharedResult(), nil)
		assert.Assert(t, state.remote)
		assert.Assert(t, state.mergedAt >= before && state.mergedAt <= after)
	})
	t.Run("local", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := transferTestCache(t)
		local := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "local"})
		state := c.lazySpanCacheState(ctx, local.cacheSharedResult(), nil)
		assert.Assert(t, !state.remote)
		assert.Equal(t, int64(0), state.mergedAt)
	})
}

// The lazy-evaluation span stamps the merge origin beside the entry it names.
func TestLazyOpSpanStampsValueRemote(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		state lazySpanCacheState
		want  map[string]string
	}{
		{name: "local", state: lazySpanCacheState{}, want: map[string]string{}},
		{name: "remote", state: lazySpanCacheState{remote: true, mergedAt: 1760000000123456789}, want: map[string]string{
			telemetryattrs.CacheValueRemoteAttr:   "true",
			telemetryattrs.CacheValueMergedAtAttr: "1760000000123456789",
		}},
		{name: "remote after a restart", state: lazySpanCacheState{remote: true}, want: map[string]string{
			telemetryattrs.CacheValueRemoteAttr: "true",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const resultID = sharedResultID(7)
			sr, rootCtx, root := newLazyRecordingRoot("POST /query")
			c := &Cache{sessionLazySpansBySession: map[string]map[sharedResultID]trace.SpanContext{}}
			evalCtx := engine.ContextWithClientMetadata(rootCtx, &engine.ClientMetadata{SessionID: "sess-value-remote", ClientID: "client-1"})
			_, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, LazyGroupWhole, &ResultCall{Field: "withExec"}, "")
			var err error
			endOTelLazyOp(lazySpan, isResume, resultID, false, false, "", tc.state, &err)
			root.End()
			exported := spanBySpanID(t, sr.Ended(), lazySpan.SpanContext().SpanID())
			got := map[string]string{}
			for _, kv := range exported.Attributes() {
				switch string(kv.Key) {
				case telemetryattrs.CacheValueRemoteAttr, telemetryattrs.CacheValueMergedAtAttr:
					got[string(kv.Key)] = kv.Value.AsString()
				}
			}
			assert.DeepEqual(t, tc.want, got)
		})
	}
}

// The lazy-evaluation span stamps the entry's wait for a remote lookup's
// merge, when one ran.
func TestLazyOpSpanStampsRemoteLookupWait(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		state lazySpanCacheState
		want  map[string]string
	}{
		{name: "no wait", state: lazySpanCacheState{}, want: map[string]string{}},
		{name: "waited", state: lazySpanCacheState{lookupWait: &remoteLookupWait{waited: 1500 * time.Millisecond, outcome: "deadline"}}, want: map[string]string{
			telemetryattrs.CacheRemoteLookupWaitedAttr:      "1500000000",
			telemetryattrs.CacheRemoteLookupWaitOutcomeAttr: "deadline",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const resultID = sharedResultID(7)
			sr, rootCtx, root := newLazyRecordingRoot("POST /query")
			c := &Cache{sessionLazySpansBySession: map[string]map[sharedResultID]trace.SpanContext{}}
			evalCtx := engine.ContextWithClientMetadata(rootCtx, &engine.ClientMetadata{SessionID: "sess-lookup-wait", ClientID: "client-1"})
			_, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, LazyGroupWhole, &ResultCall{Field: "withExec"}, "")
			var err error
			endOTelLazyOp(lazySpan, isResume, resultID, false, false, "", tc.state, &err)
			root.End()
			exported := spanBySpanID(t, sr.Ended(), lazySpan.SpanContext().SpanID())
			got := map[string]string{}
			for _, kv := range exported.Attributes() {
				switch string(kv.Key) {
				case telemetryattrs.CacheRemoteLookupWaitedAttr, telemetryattrs.CacheRemoteLookupWaitOutcomeAttr:
					got[string(kv.Key)] = kv.Value.AsString()
				}
			}
			assert.DeepEqual(t, tc.want, got)
		})
	}
}

// The new facts never change a decision: the same calls with no carrier get
// the same results.
func TestCacheEvidenceRemoteFactsChangeNoDecision(t *testing.T) {
	t.Parallel()
	for _, armed := range []bool{false, true} {
		bundle, _ := mergeTestSource(t, "root", "sent")
		ctx, c, srv := transferTestCache(t)
		var once sync.Once
		var mergeErr error
		c.testBeforePublicationIndex = func(*ongoingCall) {
			once.Do(func() { _, mergeErr = c.MergeValues(ctx, cloudCacheID, bundle) })
		}
		frame := evidenceRemoteTestRootFrame()
		req := &CallRequest{ResultCall: frame, IsPersistable: true}
		if armed {
			req.CacheEvidence = NewCacheDecision()
		}
		calls := 0
		res, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
			calls++
			return NewResultForCall(&transferTestValue{Text: "computed"}, frame)
		})
		assert.NilError(t, err)
		assert.NilError(t, mergeErr)
		assert.Equal(t, 1, calls)
		assert.Assert(t, !res.HitCache())
		c.egraphMu.RLock()
		imported := res.cacheSharedResult().imported
		c.egraphMu.RUnlock()
		assert.Assert(t, imported, "armed=%v: the merged entry is served either way", armed)
	}
}
