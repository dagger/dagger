package dagql

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// newPlanFixture is a prefetch fixture whose offer waits in its lane, which
// imports nothing ahead: the ahead queue is on, and takes only offers larger
// than any here.
func newPlanFixture(t *testing.T, cfg func(*partPrefetchConfig)) *prefetchFixture {
	t.Helper()
	return newPrefetchFixture(t, true, func(config *partPrefetchConfig) {
		config.aheadOffer = 1 << 40
		if cfg != nil {
			cfg(config)
		}
	})
}

// plannedJobs are the jobs in the prefetcher's planned queue.
func (f *prefetchFixture) plannedJobs() []*partPrefetchJob {
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	return append([]*partPrefetchJob(nil), f.prefetch.planned...)
}

// awaitPlanned waits for the offer's job to reach the planned queue, and
// returns it.
func (f *prefetchFixture) awaitPlanned(t *testing.T) *partPrefetchJob {
	t.Helper()
	var job *partPrefetchJob
	require.Eventually(t, func() bool {
		for _, planned := range f.plannedJobs() {
			if planned.offer == f.offer {
				job = planned
				return true
			}
		}
		return false
	}, 10*time.Second, 5*time.Millisecond, "the offer was never planned")
	return job
}

// runPlanned runs the planned queue's jobs of the offer, as a planned worker
// does.
func (f *prefetchFixture) runPlanned(t *testing.T) {
	t.Helper()
	job := f.awaitPlanned(t)
	f.prefetch.mu.Lock()
	f.prefetch.planned = nil
	f.prefetch.mu.Unlock()
	f.prefetch.runAhead([]*partPrefetchJob{job})
}

// A planned part is fetched at once: its offer leaves its lane for the
// planned queue, its blobs are downloaded and its chain is imported ahead,
// which its lane would not have done, and its install unpacks and downloads
// nothing. The plan worker logs what it did: one plan, which found the
// entry's one offer, with the graph lock taken once to find the row and once
// to find its offer.
func TestPlannedPartFetchAtOnce(t *testing.T) {
	f := newPlanFixture(t, nil)
	f.cache.PlanPartFetch(f.ctx, uint64(f.receiver.cacheSharedResult().id), f.address.Part)
	job := f.awaitPlanned(t)
	require.True(t, job.planned.Load())
	f.prefetch.mu.Lock()
	require.Empty(t, f.prefetch.queues[job.lane], "the offer left its lane")
	f.prefetch.mu.Unlock()
	f.runPlanned(t)
	ahead := f.aheadLine(t)
	require.Equal(t, "done", ahead.fields["outcome"])
	require.EqualValues(t, 2, ahead.int(t, "unpacked_layers"))
	require.NoError(t, f.run())
	require.True(t, f.installed())
	installs := f.logs.lines(t, "remote cache part installed")
	require.Len(t, installs, 1)
	require.Zero(t, installs[0].int(t, "bytes"), "the install downloads nothing")
	require.EqualValues(t, 2, f.store.Applies.Load(), "the install unpacks nothing")
	var line offerLine
	require.Eventually(t, func() bool {
		lines := f.logs.lines(t, "remote cache part plans fired")
		if len(lines) == 0 {
			return false
		}
		line = lines[0]
		return true
	}, 10*time.Second, 5*time.Millisecond)
	require.EqualValues(t, 1, line.int(t, "plans"))
	require.EqualValues(t, 1, line.int(t, "offers"))
	require.EqualValues(t, 0, line.int(t, "none"))
	require.EqualValues(t, 2, line.int(t, "graph_holds"))
}

// A part planned with no part key is a read of the whole entry: each of its
// parts is fetched.
func TestPlannedPartFetchOfAWholeEntry(t *testing.T) {
	f := newPlanFixture(t, nil)
	f.cache.PlanPartFetch(f.ctx, uint64(f.receiver.cacheSharedResult().id), "")
	f.runPlanned(t)
	require.Equal(t, "done", f.aheadLine(t).fields["outcome"])
}

// A plan for an entry the cache doesn't hold fetches nothing.
func TestPlannedPartFetchOfAnEntryNotHeld(t *testing.T) {
	f := newPlanFixture(t, nil)
	f.cache.PlanPartFetch(f.ctx, 1<<40, f.address.Part)
	require.Never(t, func() bool { return len(f.plannedJobs()) > 0 }, 200*time.Millisecond, 10*time.Millisecond)
}

// A plan that fires after its entry's value was decoded, which lets go of
// the entry's persisted envelope though its content is still only offered,
// finds the entry's offer all the same, from the record an install would
// capture.
func TestPlannedPartFetchOfADecodedEntry(t *testing.T) {
	f := newPlanFixture(t, nil)
	_, err := f.cache.ensurePersistedHitValueLoaded(f.ctx, f.srv, f.receiver)
	require.NoError(t, err)
	row := f.receiver.cacheSharedResult()
	row.payloadMu.RLock()
	decoded := row.persistedEnvelope == nil && row.hasValue
	row.payloadMu.RUnlock()
	require.True(t, decoded, "the entry's value is decoded")
	f.cache.PlanPartFetch(f.ctx, uint64(row.id), f.address.Part)
	f.runPlanned(t)
	require.Equal(t, "done", f.aheadLine(t).fields["outcome"])
}

// takeLaneJob takes the offer's job from its lane, as a lane worker does
// before it runs it.
func (f *prefetchFixture) takeLaneJob(t *testing.T) *partPrefetchJob {
	t.Helper()
	job := f.takeJobUnstarted(t)
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	job.cancel = func() {}
	return job
}

// takeJobUnstarted takes the offer's job from its queue, as the ahead worker
// takes every job of an offer before it starts the first.
func (f *prefetchFixture) takeJobUnstarted(t *testing.T) *partPrefetchJob {
	t.Helper()
	job := f.job(t, f.offer)
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	require.True(t, f.prefetch.unqueueLocked(job))
	return job
}

// A plan that fires while a worker has taken the offer's job from its queue
// but not yet started it leaves the job to that worker, which then
// downloads it whatever the budget.
func TestPlannedPartFetchOfAJobTakenButNotStarted(t *testing.T) {
	f := newPlanFixture(t, func(config *partPrefetchConfig) { config.budget = 1 })
	job := f.takeJobUnstarted(t)
	f.cache.PlanPartFetch(f.ctx, uint64(f.receiver.cacheSharedResult().id), f.address.Part)
	require.Eventually(t, job.planned.Load, 10*time.Second, 5*time.Millisecond, "the taken job is marked planned")
	require.Empty(t, f.plannedJobs(), "the worker keeps it")
	f.prefetch.run(context.Background(), job)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.Equal(t, "done", lines[len(lines)-1].fields["outcome"])
}

// A plan that fires while a lane worker has taken the offer's job, before
// the job meets the budget, leaves the job to that worker, which then
// downloads it whatever the budget.
func TestPlannedPartFetchOfAJobALaneWorkerTook(t *testing.T) {
	f := newPlanFixture(t, func(config *partPrefetchConfig) { config.budget = 1 })
	job := f.takeLaneJob(t)
	f.cache.PlanPartFetch(f.ctx, uint64(f.receiver.cacheSharedResult().id), f.address.Part)
	require.Eventually(t, func() bool {
		f.prefetch.mu.Lock()
		defer f.prefetch.mu.Unlock()
		return job.planned.Load()
	}, 10*time.Second, 5*time.Millisecond, "the taken job is marked planned")
	require.Empty(t, f.plannedJobs(), "the worker keeps it")
	f.prefetch.run(context.Background(), job)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.Equal(t, "done", lines[len(lines)-1].fields["outcome"])
	require.Equal(t, "done", f.aheadLine(t).fields["outcome"])
}

// A plan that fires once a lane worker's job was skipped against the budget,
// before the worker let go of it, queues a planned job in its place, which
// downloads it.
func TestPlannedPartFetchAfterItsLaneJobWasSkipped(t *testing.T) {
	f := newPlanFixture(t, func(config *partPrefetchConfig) { config.budget = 1 })
	job := f.takeLaneJob(t)
	f.prefetch.run(context.Background(), job)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.Equal(t, "skipped_budget", lines[len(lines)-1].fields["outcome"])
	f.cache.PlanPartFetch(f.ctx, uint64(f.receiver.cacheSharedResult().id), f.address.Part)
	planned := f.awaitPlanned(t)
	require.NotSame(t, job, planned)
	f.prefetch.mu.Lock()
	if key := job.jobKey(); f.prefetch.jobs[key] == job {
		delete(f.prefetch.jobs, key)
	}
	f.prefetch.mu.Unlock()
	f.runPlanned(t)
	lines = f.logs.lines(t, "remote cache part prefetched")
	require.Equal(t, "done", lines[len(lines)-1].fields["outcome"])
}

// The prefetch budget bounds what fetching ahead holds on a guess; a planned
// part is downloaded whatever the budget, as its lane's job would not be.
func TestPlannedPartFetchIgnoresTheBudget(t *testing.T) {
	f := newPlanFixture(t, func(config *partPrefetchConfig) { config.budget = 1 })
	f.cache.PlanPartFetch(f.ctx, uint64(f.receiver.cacheSharedResult().id), f.address.Part)
	f.runPlanned(t)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.NotEmpty(t, lines)
	require.Equal(t, "done", lines[len(lines)-1].fields["outcome"])
	require.Equal(t, true, lines[len(lines)-1].fields["planned"])
}

// planDelegatePayload is planDelegateCodec's payload: the entry whose part
// at the same address the payload's parts delegate to.
type planDelegatePayload struct {
	Parent uint64 `json:"parent"`
}

type planDelegateValue struct{}

func (planDelegateValue) Type() *ast.Type { return nil }

// planDelegateCodec routes every part to the same part of its payload's
// parent, as a container's filesystem delegates to its parent's when it is
// unchanged.
type planDelegateCodec struct{}

func (planDelegateCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: append([]byte(nil), v.Payload...)}, nil
}
func (planDelegateCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }
func (planDelegateCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	return []CapturedCodecOutput{{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "pending", ValueKind: "directory"}}, nil
}
func (planDelegateCodec) RouteParts(v PersistedPayloadVisit, part PartKey) (LazyOperationRoute, error) {
	var p planDelegatePayload
	if err := json.Unmarshal(v.Payload, &p); err != nil {
		return LazyOperationRoute{}, err
	}
	return LazyOperationRoute{Delegation: &PartDelegation{ParentResultID: p.Parent, Address: PersistedPartAddress{Part: part}}}, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.PlanDelegate", Typed: planDelegateValue{}, Visitor: PersistedNoReferences{}, Transfer: planDelegateCodec{}})
}

// A planned part an entry delegates to its recorded parent, which holds no
// offer of its own, is fetched from the offer that serves the parent's part,
// as the entry's install would find it.
func TestPlannedPartFetchFollowsDelegation(t *testing.T) {
	f := newPlanFixture(t, nil)
	raw, err := json.Marshal(planDelegatePayload{Parent: uint64(f.receiver.cacheSharedResult().id)})
	require.NoError(t, err)
	child := &sharedResult{id: 1 << 40, persistedEnvelope: &PersistedResultEnvelope{Kind: persistedResultKindObject, ObjectCodec: "dagql_test.PlanDelegate", ObjectJSON: raw}}
	f.cache.egraphMu.Lock()
	f.cache.resultsByID[child.id] = child
	f.cache.egraphMu.Unlock()
	t.Cleanup(func() {
		f.cache.egraphMu.Lock()
		delete(f.cache.resultsByID, child.id)
		f.cache.egraphMu.Unlock()
	})

	f.cache.PlanPartFetch(f.ctx, uint64(child.id), f.address.Part)
	job := f.awaitPlanned(t)
	require.Equal(t, f.receiver.cacheSharedResult().id, job.row, "the job is the parent's offer's")
}
