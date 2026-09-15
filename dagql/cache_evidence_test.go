package dagql

// Cache-evidence carrier population tests: drive GetOrInitCall directly with an
// armed CacheDecision (simulating core.AroundFunc's allocation) and assert the
// decision facts recorded for every cache route. dagql tests never observe
// spans — attribute mapping is core's seam and is tested there.

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql/call"
)

func cacheTestArmedRequest(frame *ResultCall) *CallRequest {
	return &CallRequest{
		ResultCall:    frame,
		CacheEvidence: NewCacheDecision(),
	}
}

func cacheTestEvidenceEnv(t *testing.T) (context.Context, *Cache) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	return ContextWithCache(ctx, cacheIface), cacheIface
}

func TestCacheEvidenceExecutedFreshCall(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-executed-fresh")
	req := cacheTestArmedRequest(frame)
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
	assert.Equal(t, CacheHitRoute(""), ev.HitRoute)
	assert.Assert(t, !ev.MissIncompatibleCandidates)
	assert.Assert(t, !ev.MissSawExpired)
	assert.Equal(t, -1, ev.MissUnknownInputIndex)
	assert.Assert(t, ev.SelfDigest != "")
	assert.Equal(t, 0, len(ev.StructuralInputs))
	// No implicit inputs: the pairing digest equals the self digest by
	// construction.
	assert.Equal(t, ev.SelfDigest, ev.PairingDigest)
}

func TestCacheEvidenceRecipeHit(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-recipe-hit")
	_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	assert.NilError(t, err)

	req := cacheTestArmedRequest(frame)
	initCalls := 0
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(frame, 2), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, res.HitCache())

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeHit, ev.Outcome)
	assert.Equal(t, CacheHitRouteRecipe, ev.HitRoute)
	assert.Assert(t, !ev.MissIncompatibleCandidates)
	assert.Assert(t, !ev.MissSawExpired)
	assert.Equal(t, -1, ev.MissUnknownInputIndex)
	assert.Assert(t, ev.SelfDigest != "")
}

func TestCacheEvidenceDigestRouteHit(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	contentDigest := digest.FromString("evidence-digest-route-content")
	producer := cacheTestIntCall("evidence-digest-route-producer")
	_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: producer}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(producer, 7).(Result[Int]).WithContentDigest(ctx, contentDigest)
	})
	assert.NilError(t, err)

	// A different operation whose request carries the produced content digest
	// as equivalence evidence: recipe lookup misses, extra-digest lookup hits.
	consumer := cacheTestIntCall("evidence-digest-route-consumer", call.ExtraDigest{
		Label:  call.ExtraDigestLabelContent,
		Digest: contentDigest,
	})
	req := cacheTestArmedRequest(consumer)
	initCalls := 0
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(consumer, 8), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, res.HitCache())

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeHit, ev.Outcome)
	assert.Equal(t, CacheHitRouteDigest, ev.HitRoute)
}

func TestCacheEvidenceStructuralRouteHit(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	digA := digest.FromString("evidence-structural-input-a")
	digB := digest.FromString("evidence-structural-input-b")
	structuralFrame := func(src digest.Digest) *ResultCall {
		frame := cacheTestIntCall("evidence-structural-op")
		frame.Args = []*ResultCallArg{{
			Name: "src",
			Value: &ResultCallLiteral{
				Kind:                 ResultCallLiteralKindDigestedString,
				DigestedStringValue:  "witnessed",
				DigestedStringDigest: src,
			},
		}}
		return frame
	}

	frameA := structuralFrame(digA)
	_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frameA}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frameA, 1), nil
	})
	assert.NilError(t, err)

	// Teach digA ≡ digB by publishing a result carrying both as extra digests:
	// publication accumulates them onto one output eq-class.
	teacher := cacheTestIntCall("evidence-structural-teacher",
		call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: digA},
		call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: digB},
	)
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: teacher}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(teacher, 2), nil
	})
	assert.NilError(t, err)

	// Same operation over the equivalent input: different recipe digest, same
	// self digest, input digests in one eq-class → structural term hit.
	frameB := structuralFrame(digB)
	req := cacheTestArmedRequest(frameB)
	initCalls := 0
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(frameB, 3), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, res.HitCache())

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeHit, ev.Outcome)
	assert.Equal(t, CacheHitRouteStructural, ev.HitRoute)
	assert.Equal(t, 1, len(ev.StructuralInputs))
	assert.Equal(t, digB, ev.StructuralInputs[0])
}

func TestCacheEvidenceMissSawExpired(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-saw-expired")
	res1, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, TTL: 3600}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	assert.NilError(t, err)
	shared := res1.cacheSharedResult()
	assert.Assert(t, shared != nil)
	// Force the published result past its TTL without sleeping.
	c.egraphMu.Lock()
	shared.expiresAtUnix = time.Now().Add(-time.Hour).Unix()
	c.egraphMu.Unlock()

	req := cacheTestArmedRequest(frame)
	req.TTL = 3600
	initCalls := 0
	res2, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(frame, 2), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, !res2.HitCache())

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
	assert.Assert(t, ev.MissSawExpired)
	// Expiry dropped the only candidate during accumulation, so no incompatible
	// (session-rejected) candidates remained.
	assert.Assert(t, !ev.MissIncompatibleCandidates)
	assert.Equal(t, -1, ev.MissUnknownInputIndex)
}

func TestCacheEvidenceSawExpiredThroughEqClassExpansion(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	// Build the state where appendTermSetResultsLocked's SECOND loop — the
	// output-eq-class digest expansion — is the only place expiry can be
	// observed: the term survives with no directly-associated results (its
	// observed result is collected), while the term's output eq-class still
	// indexes an equivalent result that has expired.
	contentDigest := digest.FromString("evidence-exp-content")
	inputDigest := digest.FromString("evidence-exp-input")
	consumerFrame := func() *ResultCall {
		frame := cacheTestIntCall("evidence-exp-consumer")
		frame.Args = []*ResultCallArg{{
			Name: "src",
			Value: &ResultCallLiteral{
				Kind:                 ResultCallLiteralKindDigestedString,
				DigestedStringValue:  "witnessed",
				DigestedStringDigest: inputDigest,
			},
		}}
		return frame
	}

	// 1. Observe the term once, in a scratch session, and teach its result a
	// content digest so its output eq-class gains a second identity route.
	scratchCtx := ContextWithCache(engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "dagql-test-client-exp",
		SessionID: "evidence-exp-session",
	}), c)
	frameA := consumerFrame()
	resC, err := c.GetOrInitCall(scratchCtx, "evidence-exp-session", noopTypeResolver{}, &CallRequest{ResultCall: frameA}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frameA, 1), nil
	})
	assert.NilError(t, err)
	resC, err = resC.WithContentDigestAny(scratchCtx, contentDigest)
	assert.NilError(t, err)
	collectedID := resC.cacheSharedResult().id

	// 2. An equivalent-by-content sibling result, owned by the main session,
	// indexed under the shared content digest (same output eq-class).
	producerFrame := cacheTestIntCall("evidence-exp-producer")
	resP, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: producerFrame}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(producerFrame, 2), nil
	})
	assert.NilError(t, err)
	resP, err = resP.WithContentDigestAny(ctx, contentDigest)
	assert.NilError(t, err)

	// 3. Collect the term's observed result: release its only owning session,
	// then prove it is gone so the first accumulation loop (direct term
	// results) has nothing at all — live or expired — to visit.
	assert.NilError(t, c.ReleaseSession(scratchCtx, "evidence-exp-session"))
	c.egraphMu.Lock()
	_, stillLive := c.resultsByID[collectedID]
	c.egraphMu.Unlock()
	assert.Assert(t, !stillLive)

	// 4. Expire the surviving eq-class sibling.
	c.egraphMu.Lock()
	resP.cacheSharedResult().expiresAtUnix = time.Now().Add(-time.Hour).Unix()
	c.egraphMu.Unlock()

	// 5. Re-issue the consumer call: recipe index no longer holds the
	// collected result, the request carries no extra digests, so the lookup
	// reaches the structural term — whose only candidate route is the
	// eq-class expansion, where the expired sibling is dropped.
	frameB := consumerFrame()
	req := cacheTestArmedRequest(frameB)
	initCalls := 0
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(frameB, 3), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, !res.HitCache())

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
	assert.Assert(t, ev.MissSawExpired)
	assert.Assert(t, !ev.MissIncompatibleCandidates)
	assert.Equal(t, -1, ev.MissUnknownInputIndex)
}

func TestCacheEvidenceMissIncompatibleCandidates(t *testing.T) {
	t.Parallel()
	baseCtx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface
	ctxA := ContextWithCache(baseCtx, c)

	handle := SessionResourceHandle("evidence-incompatible-secret")
	assert.NilError(t, c.BindSessionResource(ctxA, "test-session", "dagql-test-client", handle, "concrete"))

	frame := cacheTestIntCall("evidence-incompatible")
	_, err = c.GetOrInitCall(ctxA, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1).(Result[Int]).WithSessionResourceHandleAny(ctxA, handle)
	})
	assert.NilError(t, err)

	// A second session that never bound the handle: the recipe candidate
	// exists but fails the session-resource filter.
	ctxB := ContextWithCache(engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "dagql-test-client-b",
		SessionID: "test-session-b",
	}), c)
	req := cacheTestArmedRequest(frame)
	initCalls := 0
	res, err := c.GetOrInitCall(ctxB, "test-session-b", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(frame, 2).(Result[Int]).WithSessionResourceHandleAny(ctxB, handle)
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, !res.HitCache())

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
	assert.Assert(t, ev.MissIncompatibleCandidates)
	assert.Assert(t, !ev.MissSawExpired)
}

func TestCacheEvidenceJoinedAndExecutedCarriers(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-joined")
	release := make(chan struct{})
	started := make(chan struct{})
	execDone := make(chan error, 1)

	// Executor: same concurrency key, blocks inside the resolver until the
	// joiner has been admitted.
	execReq := cacheTestArmedRequest(frame)
	execReq.ConcurrencyKey = "evidence-joined-key"
	go func() {
		_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, execReq, func(context.Context) (AnyResult, error) {
			close(started)
			<-release
			return cacheTestIntResult(frame, 1), nil
		})
		execDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for executor start")
	}

	// Joiner: identical call and key, its own request and its own carrier.
	joinReq := cacheTestArmedRequest(frame)
	joinReq.ConcurrencyKey = "evidence-joined-key"
	joinDone := make(chan error, 1)
	var joinRes AnyResult
	go func() {
		var err error
		joinRes, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, joinReq, func(context.Context) (AnyResult, error) {
			t.Error("joiner resolver must not run")
			return cacheTestIntResult(frame, 2), nil
		})
		joinDone <- err
	}()

	// Wait for the joiner's admission via the mutex-guarded waiter count (the
	// carrier itself is single-goroutine state owned by each invocation and
	// must not be read while its invocation runs), then release the shared
	// execution. Carriers are only read after both goroutines complete.
	waitKeys := callConcurrencyKeys{
		callKey:        cacheTestCallDigest(frame).String(),
		concurrencyKey: "evidence-joined-key",
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.callsMu.Lock()
		waiters := 0
		if oc := c.ongoingCalls[waitKeys]; oc != nil {
			waiters = oc.waiters
		}
		c.callsMu.Unlock()
		if waiters >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for joiner admission")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	assert.NilError(t, <-execDone)
	assert.NilError(t, <-joinDone)

	assert.Equal(t, CacheOutcomeExecuted, execReq.CacheEvidence.Outcome)
	assert.Equal(t, CacheOutcomeJoined, joinReq.CacheEvidence.Outcome)
	// Per-invocation carriers: distinct records, one shared execution.
	assert.Assert(t, execReq.CacheEvidence != joinReq.CacheEvidence)
	assert.Assert(t, joinRes != nil)
	// Both carry the same identity facts, derived independently.
	assert.Equal(t, execReq.CacheEvidence.SelfDigest, joinReq.CacheEvidence.SelfDigest)
	assert.Equal(t, execReq.CacheEvidence.PairingDigest, joinReq.CacheEvidence.PairingDigest)
}

func TestCacheEvidenceDoNotCacheUncached(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-uncached")
	req := cacheTestArmedRequest(frame)
	req.DoNotCache = true
	_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	assert.NilError(t, err)

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeUncached, ev.Outcome)
	// The DoNotCache path skips identity derivation entirely.
	assert.Equal(t, digest.Digest(""), ev.SelfDigest)
	assert.Equal(t, digest.Digest(""), ev.PairingDigest)
	assert.Equal(t, 0, len(ev.StructuralInputs))
}

func TestCacheEvidencePendingLazyHitIsHit(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-pending-lazy")
	_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		res, err := NewResultForCall(&cacheTestObject{
			Value:    1,
			lazyEval: func(context.Context) error { return nil },
		}, frame)
		if err != nil {
			return nil, err
		}
		return res, nil
	})
	assert.NilError(t, err)

	req := cacheTestArmedRequest(frame)
	res2, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		t.Error("resolver must not run on a lazy-shell hit")
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	// The lookup fact is a hit even though deferred work is still pending —
	// the evaluation fact stays separate (dag.pending semantics unchanged).
	assert.Assert(t, HasPendingLazyEvaluation(res2))
	assert.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
	assert.Equal(t, CacheHitRouteRecipe, req.CacheEvidence.HitRoute)
}

func TestCacheEvidenceUnknownInputIndex(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-unknown-input")
	frame.Args = []*ResultCallArg{{
		Name: "src",
		Value: &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  "never-seen",
			DigestedStringDigest: digest.FromString("evidence-unknown-input-digest"),
		},
	}}
	req := cacheTestArmedRequest(frame)
	_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	assert.NilError(t, err)

	ev := req.CacheEvidence
	assert.Equal(t, CacheOutcomeExecuted, ev.Outcome)
	// The digest witness had no eq-class yet, so the structural lookup was
	// impossible; the index points into the recorded structural-input list.
	assert.Equal(t, 0, ev.MissUnknownInputIndex)
	assert.Equal(t, 1, len(ev.StructuralInputs))
	assert.Assert(t, !ev.MissIncompatibleCandidates)
}

func TestCacheEvidencePairingDigestExcludesImplicitInputs(t *testing.T) {
	t.Parallel()

	frameWithImplicit := func(scope string) *ResultCall {
		frame := cacheTestIntCall("evidence-pairing")
		frame.ImplicitInputs = []*ResultCallArg{{
			Name: "cachePerClient",
			Value: &ResultCallLiteral{
				Kind:        ResultCallLiteralKindString,
				StringValue: scope,
			},
		}}
		return frame
	}
	frameA := frameWithImplicit("client-a")
	frameB := frameWithImplicit("client-b")
	framePlain := cacheTestIntCall("evidence-pairing")

	selfA, _, err := frameA.selfDigestAndInputRefs(nil)
	assert.NilError(t, err)
	selfB, _, err := frameB.selfDigestAndInputRefs(nil)
	assert.NilError(t, err)
	selfPlain, _, err := framePlain.selfDigestAndInputRefs(nil)
	assert.NilError(t, err)
	pairA, err := frameA.pairingDigest(nil)
	assert.NilError(t, err)
	pairB, err := frameB.pairingDigest(nil)
	assert.NilError(t, err)
	pairPlain, err := framePlain.pairingDigest(nil)
	assert.NilError(t, err)

	// Implicit inputs scope the self digest but never the pairing digest.
	assert.Assert(t, selfA != selfB)
	assert.Equal(t, pairA, pairB)
	// A frame with no implicit inputs pairs and selfs identically, and the
	// scoped frames pair to exactly that unscoped identity.
	assert.Equal(t, selfPlain, pairPlain)
	assert.Equal(t, pairA, pairPlain)
	// Sensitive-arg redaction applies to both derivations equally: a sensitive
	// literal changes neither digest's relationship, and both redact.
	sensitiveA := frameWithImplicit("client-a")
	sensitiveA.Args = []*ResultCallArg{{
		Name:        "token",
		IsSensitive: true,
		Value: &ResultCallLiteral{
			Kind:        ResultCallLiteralKindString,
			StringValue: "secret-one",
		},
	}}
	sensitiveB := frameWithImplicit("client-a")
	sensitiveB.Args = []*ResultCallArg{{
		Name:        "token",
		IsSensitive: true,
		Value: &ResultCallLiteral{
			Kind:        ResultCallLiteralKindString,
			StringValue: "secret-two",
		},
	}}
	sensPairA, err := sensitiveA.pairingDigest(nil)
	assert.NilError(t, err)
	sensPairB, err := sensitiveB.pairingDigest(nil)
	assert.NilError(t, err)
	assert.Equal(t, sensPairA, sensPairB)
}

func TestCacheEvidenceCloneDropsCarrier(t *testing.T) {
	t.Parallel()

	req := cacheTestArmedRequest(cacheTestIntCall("evidence-clone"))
	assert.Assert(t, req.CacheEvidence != nil)
	clone := req.Clone()
	assert.Assert(t, clone.CacheEvidence == nil)
}

func TestCacheEvidenceNilCarrierIsNoop(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)

	frame := cacheTestIntCall("evidence-nil-carrier")
	// Executed, hit, and uncached paths all run with a nil carrier.
	res1, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res1.HitCache())
	res2, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 2), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, DoNotCache: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 3), nil
	})
	assert.NilError(t, err)
}
