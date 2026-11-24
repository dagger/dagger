package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// endedGroupPublish runs publishEvaluatedParts as its real owner does, but
// ends the original's group between BeginOriginal and the publication, as
// the evaluation having ended does. installed says whether the part the
// original was to produce is installed by then.
func endedGroupPublish(t *testing.T, installed bool) error {
	t.Helper()
	ctx, c, srv, _ := shareTestCache(t)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	receiver := persistedListTestResult(t, ctx, c, srv, "ended-receiver", newShareTestValue("receiver", map[string]sharePartState{"fs": {}}))
	shareTestEncodedReceiver(t, ctx, c, receiver)
	evaluated := persistedListTestResult(t, ctx, c, srv, "ended-evaluated", newShareTestValue("evaluated", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}}))
	address := PersistedPartAddress{Part: "fs"}
	// A publication that retries in place is ended by this deadline, not by
	// the go test timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.RunLazyTask(ctx, receiver, "lazy:ended", LazyTaskSpec{Body: func(ctx context.Context) error {
		drain, _, err := c.PrepareOriginal(ctx, receiver, LazyGroupAddress{Group: LazyGroupWhole}, []PersistedPartAddress{address}, PartTaskFromContext(ctx))
		if err != nil {
			return err
		}
		if err = drain.Wait(ctx); err != nil {
			return err
		}
		scan, err := c.CheckPartSources(ctx, receiver, address, drain, &PartDemandState{})
		if err != nil {
			return err
		}
		original, _, err := c.BeginOriginal(ctx, scan.NoSource)
		if err != nil {
			return err
		}
		gate := original.gate
		gate.mu.Lock()
		group := gate.groups[lazyGroupAddressKey(original.group)]
		if installed {
			group.phase = LazyEvaluationEvaluated
			key, err := partAddressKey(address)
			require.NoError(t, err)
			gate.outputs[key] = partOutputState{phase: PartOutputInstalled, task: original.task}
		} else {
			group.phase = LazyEvaluationOpen
		}
		gate.revision++
		gate.mu.Unlock()
		var version capturedRowRevision
		produced, err := c.capturePartRecord(ctx, evaluated.cacheSharedResult(), false, nil, &version)
		if err != nil {
			return err
		}
		return c.publishEvaluatedParts(ctx, receiver, address, produced, original, &partCleanup{fn: func(context.Context) error { return nil }}, &PartDemandState{})
	}})
}

// When the evaluation an original began has ended and what it was to produce
// is installed, the part is there: the publication succeeds at once instead
// of retrying a refusal that nothing can change.
func TestPublishAfterEndedEvaluationFindsThePartInstalled(t *testing.T) {
	require.NoError(t, endedGroupPublish(t, true))
}

// When the evaluation ended without installing the part, the publication
// returns the refusal to the decision that began the evaluation, which
// selects again, instead of retrying in place until it is canceled.
func TestPublishAfterEndedEvaluationReturnsToTheDecision(t *testing.T) {
	err := endedGroupPublish(t, false)
	require.True(t, partCanReselect(err), "the decision selects again: %v", err)
	require.True(t, refusalEndsEvaluation(err))
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "publish: own group not running")
}
