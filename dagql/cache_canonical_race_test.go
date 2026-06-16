package dagql

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"
)

// TestCacheCanonicalEquivalentSwapRacesSessionRelease reproduces a race between
// initCompletedResult's canonical-equivalent swap and ReleaseSession.
//
// When a call's fn returns an already-attached result, initCompletedResult
// swaps oc.res to canonicalEquivalentSharedResultLocked(...) and then drops
// egraphMu without taking any ownership hold on the adopted sibling. The
// handoff-hold increment only happens later, after egraphMu is re-acquired. If
// the sibling's only owner (another session) is released in that window, the
// sibling is collected and its OnRelease runs — but the completing call still
// adopts it: indexWaitResultInEgraphLocked re-registers the collected result
// (resultsByID[res.id] = res) and the caller is handed a result whose payload
// has already been released.
//
// The test choreographs that interleaving deterministically:
//
//  1. Session A publishes resultA (recipe A, content digest D). Session B
//     publishes resultB (recipe B, same content digest D). Both land in one
//     output eq-class; resultA has the lower shared result ID, so it is the
//     canonical pick.
//  2. Session B starts call C whose fn returns the attached resultB. The fn
//     parks until the test holds egraphMu, so call C's initCompletedResult
//     blocks at the canonicalization Lock while the test owns the mutex.
//  3. A ReleaseSession(session-a) goroutine queues on egraphMu behind call C.
//  4. The test forces the mutex into starvation mode (unlock+relock makes the
//     woken call-C goroutine observe a held lock after waiting >1ms), then
//     unlocks. Starvation mode hands the mutex FIFO: call C canonicalizes
//     (picks the still-live resultA) and unlocks; the release goroutine then
//     collects resultA and runs its OnRelease; call C re-acquires and
//     resurrects the corpse.
//
// The orchestration can misfire (the test's re-lock races the woken
// goroutine), so it retries a bounded number of attempts; misfires produce a
// benign ordering that the test detects and skips.
func TestCacheCanonicalEquivalentSwapRacesSessionRelease(t *testing.T) {
	const attempts = 50

	var sawBenign, sawAdoptedAlive int
	for attempt := range attempts {
		adopted, released := runCanonicalSwapReleaseAttempt(t, attempt)
		switch {
		case adopted && released:
			t.Logf("attempt %d: reproduced after %d benign orderings: session B was handed the adopted sibling after its OnRelease ran", attempt, sawBenign)
			t.Fatalf("call adopted a canonical-equivalent result whose payload was already released (use-after-release of the sibling's payload)")
		case adopted:
			// Adopted the sibling and it is still live: the release must have
			// happened after the call took ownership. Correct behavior.
			sawAdoptedAlive++
		default:
			// The release won the race entirely (sibling collected before the
			// canonicalization pick), so the call kept its own result. Correct
			// behavior, but not the interleaving under test.
			sawBenign++
		}
	}
	t.Logf("no reproduction in %d attempts (%d benign release-first orderings, %d adopted-alive orderings)", attempts, sawBenign, sawAdoptedAlive)
}

// runCanonicalSwapReleaseAttempt runs one choreographed attempt. It reports
// whether call C's returned result adopted session A's sibling result, and
// whether that sibling's OnRelease had run by the time both goroutines
// finished.
func runCanonicalSwapReleaseAttempt(t *testing.T, attempt int) (adoptedSibling bool, siblingReleased bool) {
	t.Helper()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, c)

	contentDigest := digest.FromString("canonical-swap-race-content")

	// Session A publishes the canonical sibling: lowest shared result ID,
	// owned only by session A's session edge.
	var releasedA atomic.Bool
	callA := cacheTestIntCall("canonical-swap-race-a")
	resA, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: callA}, func(ctx context.Context) (AnyResult, error) {
		detached := cacheTestDetachedResult(callA, cacheTestOnReleaseInt{
			Int: NewInt(1),
			onRelease: func(context.Context) error {
				releasedA.Store(true)
				return nil
			},
		})
		return detached.WithContentDigest(ctx, contentDigest)
	})
	assert.NilError(t, err)
	sharedA := resA.cacheSharedResult()
	assert.Assert(t, sharedA != nil && sharedA.id != 0)

	// Session B publishes an equivalent result (same content digest, different
	// recipe), merging both into one output eq-class.
	callB := cacheTestIntCall("canonical-swap-race-b")
	resB, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: callB}, func(ctx context.Context) (AnyResult, error) {
		detached := cacheTestDetachedResult(callB, NewInt(2))
		return detached.WithContentDigest(ctx, contentDigest)
	})
	assert.NilError(t, err)
	sharedB := resB.cacheSharedResult()
	assert.Assert(t, sharedB != nil && sharedB.id != 0)
	assert.Assert(t, sharedA.id < sharedB.id, "result A must be the canonical (lowest-ID) sibling")

	// Call C in session B: fn returns the already-attached resultB, which
	// drives initCompletedResult through the canonical-equivalent swap.
	fnEntered := make(chan struct{})
	fnRelease := make(chan struct{})
	type callOutcome struct {
		res AnyResult
		err error
	}
	callDone := make(chan callOutcome, 1)
	callC := cacheTestIntCall("canonical-swap-race-c")
	go func() {
		res, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: callC}, func(ctx context.Context) (AnyResult, error) {
			close(fnEntered)
			<-fnRelease
			return resB, nil
		})
		callDone <- callOutcome{res, err}
	}()
	<-fnEntered

	// Park call C's initCompletedResult at its first egraphMu acquisition (the
	// canonicalization Lock) by holding the mutex before letting fn return.
	c.egraphMu.Lock()
	close(fnRelease)
	time.Sleep(5 * time.Millisecond)

	// Queue the session-A release behind call C.
	releaseDone := make(chan error, 1)
	go func() {
		releaseDone <- c.ReleaseSession(context.WithoutCancel(ctx), "session-a")
	}()
	time.Sleep(5 * time.Millisecond)

	// Force the mutex into starvation mode so the upcoming unlocks hand off
	// FIFO and call C cannot barge back in between its canonicalization unlock
	// and its publication re-lock: briefly unlock and immediately re-lock so
	// the woken call-C goroutine observes a held lock after waiting >1ms and
	// re-parks with the starvation bit set.
	c.egraphMu.Unlock()
	c.egraphMu.Lock()
	time.Sleep(5 * time.Millisecond)

	// Let the interleaving run: call C canonicalizes (picking resultA), the
	// release collects resultA, then call C resurrects it.
	c.egraphMu.Unlock()

	assert.NilError(t, <-releaseDone)
	got := <-callDone
	assert.NilError(t, got.err)
	gotShared := got.res.cacheSharedResult()
	assert.Assert(t, gotShared != nil)

	adoptedSibling = gotShared == sharedA
	siblingReleased = releasedA.Load()

	if adoptedSibling {
		c.egraphMu.Lock()
		registered := c.resultsByID[sharedA.id] == sharedA
		ownership := sharedA.incomingOwnershipCount
		c.egraphMu.Unlock()
		t.Logf("attempt %d: call C adopted result A (id=%d): payloadReleased=%v reRegistered=%v ownership=%d",
			attempt, sharedA.id, siblingReleased, registered, ownership)
	}

	return adoptedSibling, siblingReleased
}
