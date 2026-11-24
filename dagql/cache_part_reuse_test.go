package dagql

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// partReuseTestValue gives a sharing test value the acquisition entry point
// of a core Directory: a demand of it binds its part host.
type partReuseTestValue struct {
	*shareTestValue
	host *PartHost
}

func (*partReuseTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "PartReuseTestValue", NonNull: true}
}

func (v *partReuseTestValue) BindPartHost(host *PartHost) { v.host = host }

func (*partReuseTestValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value, err := (*shareTestValue)(nil).DecodePersistedObject(ctx, dec, raw)
	if err != nil {
		return nil, err
	}
	return &partReuseTestValue{shareTestValue: value.(*shareTestValue)}, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.PartReuse", Typed: (*partReuseTestValue)(nil),
		Visitor: shareTestCodec{}, Transfer: shareTestCodec{}, BackgroundDecode: true,
	})
}

// partReuseCase is an imported receiver whose snapshot part was installed from a
// local donor and settled, in cache c; evaluate evaluates that exact row, as
// a step of a later build reads it; and tasks counts the part tasks that have
// finished on the row. An evaluation that reuses the part runs none.
type partReuseCase struct {
	c        *Cache
	receiver AnyResult
	evaluate func() error
	tasks    func() int
}

// partReuseTestReceiver returns a partReuseCase, with setup applied to its cache
// first.
func partReuseTestReceiver(t *testing.T, setup ...func(*Cache)) partReuseCase {
	t.Helper()
	ctx, c, srv, _ := shareTestCache(t)
	for _, f := range setup {
		f(c)
	}
	var receiverRow atomic.Pointer[sharedResult]
	var tasks atomic.Int64
	after := c.testAfterLazyEvalFinish
	c.testAfterLazyEvalFinish = func(attempt *lazyEvalAttempt) {
		if attempt.token != nil && attempt.token.row == receiverRow.Load() {
			tasks.Add(1)
		}
		if after != nil {
			after(attempt)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	t.Cleanup(cancel)
	srv.InstallObject(NewClass(srv, ClassOpts[*partReuseTestValue]{}))
	barrier := newSharePassBarrier(c)
	held := make(chan struct{})
	release := sync.OnceFunc(func() { close(held) })
	barrier.hold.Store(&held)
	t.Cleanup(release)
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &partReuseTestValue{shareTestValue: newShareTestValue("donor", map[string]sharePartState{"snapshot": {Snapshot: "local-snapshot"}})})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &partReuseTestValue{shareTestValue: newShareTestValue("receiver", map[string]sharePartState{"snapshot": {}})})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	receiverRow.Store(receiver.cacheSharedResult())
	shareTestUnite(t, ctx, c, "part-reuse", donor, receiver)
	require.Equal(t, 1, barrier.awaitEntered(t))
	// The row itself, not an equivalent a load by ID could select instead:
	// the donor is complete and would exercise nothing of the receiver's.
	evaluate := func() error {
		loaded, err := c.ensurePersistedHitValueLoaded(ctx, srv, Result[Typed]{shared: receiver.cacheSharedResult(), hitCache: true})
		if err != nil {
			return err
		}
		return c.Evaluate(ctx, loaded)
	}
	require.NoError(t, evaluate())
	release()
	require.Equal(t, 1, barrier.awaitPass(t))
	require.True(t, shareTestHasLink(receiver, "local-snapshot"))
	return partReuseCase{c: c, receiver: receiver, evaluate: evaluate, tasks: func() int { return int(tasks.Load()) }}
}

// testMovePayload moves res's payload revision, as a change to its payload
// made by another goroutine would.
func testMovePayload(res *sharedResult) {
	res.payloadMu.Lock()
	res.payloadRevision++
	res.payloadMu.Unlock()
}

// An acquired part already complete and opened is read again without
// another acquisition, as a local result is read without evaluating it
// again.
func TestAcquiredPartReuseRunsNoTask(t *testing.T) {
	t.Parallel()
	p := partReuseTestReceiver(t)
	evaluate, tasks := p.evaluate, p.tasks
	// The first read after the installation settled finds the part complete
	// and opens it.
	require.NoError(t, evaluate())
	before := tasks()
	require.NoError(t, evaluate())
	require.NoError(t, evaluate())
	require.Equal(t, before, tasks(), "a reused part runs no acquisition task")
}

// Anything that changes the row after the part was opened makes the next
// read acquire it again, and the read after that reuse it.
func TestAcquiredPartReuseEndsWhenTheRowChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*sharedResult)
	}{
		{"stored payload", testMovePayload},
		{"offers", func(row *sharedResult) { row.transferRevision++ }},
		{"dependencies", func(row *sharedResult) { row.dependencyOwnershipRevision++ }},
		{"value replacement", func(row *sharedResult) { row.replacements++ }},
		{"gate", func(row *sharedResult) {
			gate := row.partGate.gate.Load()
			gate.mu.Lock()
			gate.revision++
			gate.mu.Unlock()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := partReuseTestReceiver(t)
			c, receiver, evaluate, tasks := p.c, p.receiver, p.evaluate, p.tasks
			require.NoError(t, evaluate())
			before := tasks()
			c.egraphMu.Lock()
			tc.change(receiver.cacheSharedResult())
			c.egraphMu.Unlock()
			require.NoError(t, evaluate())
			after := tasks()
			require.Greater(t, after, before, "a changed row is acquired again")
			require.NoError(t, evaluate())
			require.Equal(t, after, tasks(), "and then reused")
		})
	}
}

// The acquisition that installs a part moves its row, by settling the
// installation and syncing the row's owner leases over the new snapshot.
// The part is still reused by the next read, not acquired a second time.
func TestAcquiredPartReuseAfterItsOwnTaskMovesTheRow(t *testing.T) {
	t.Parallel()
	var target atomic.Pointer[sharedResult]
	var moved atomic.Bool
	p := partReuseTestReceiver(t, func(c *Cache) {
		c.testAfterLazyEvalFinish = func(attempt *lazyEvalAttempt) {
			row := target.Load()
			if row == nil || attempt.token == nil || attempt.token.row != row ||
				!strings.HasPrefix(string(attempt.token.key), "acquire:") {
				return
			}
			if moved.CompareAndSwap(false, true) {
				testMovePayload(row)
			}
		}
	})
	evaluate, tasks := p.evaluate, p.tasks
	row := p.receiver.cacheSharedResult()
	// The row changes, so the next read acquires the part again; that
	// acquisition's own task moves the row before it finishes.
	testMovePayload(row)
	target.Store(row)
	require.NoError(t, evaluate())
	require.True(t, moved.Load(), "the acquisition ran its task")
	before := tasks()
	require.NoError(t, evaluate())
	require.Equal(t, before, tasks(), "the next read reuses the part")
}

// A record is never dated later than the state it saw, and a part being
// written or a running evaluation is never reused.
func TestAcquiredPartReuseConditions(t *testing.T) {
	t.Parallel()
	_, c, _, receiver := newLazyRetryTestResult(t, nil)
	row := receiver.cacheSharedResult()
	key, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	reusable := func() bool {
		c.egraphMu.RLock()
		defer c.egraphMu.RUnlock()
		return partReusableLocked(row, []string{key})
	}

	stale := c.capturePartReuse(row)
	testMovePayload(row)
	c.recordPartReuse(row, key, stale)
	require.False(t, reusable(), "a record of a state that has since moved isn't written")

	c.recordPartReuse(row, key, c.capturePartReuse(row))
	require.True(t, reusable())

	gate := row.partGate.gate.Load()
	gate.mu.Lock()
	gate.outputs[key] = partOutputState{phase: PartOutputInstalled}
	gate.mu.Unlock()
	require.False(t, reusable(), "an installed part that hasn't settled is joined, not reused")
	gate.mu.Lock()
	delete(gate.outputs, key)
	gate.groups["whole"] = &partLazyEvaluationState{phase: LazyEvaluationRunning}
	gate.mu.Unlock()
	require.False(t, reusable(), "nothing is reused while an evaluation of the row runs")
	gate.mu.Lock()
	delete(gate.groups, "whole")
	gate.writers[&writerTicket{done: make(chan struct{})}] = &PartPermit{address: PersistedPartAddress{Part: "snapshot"}}
	gate.mu.Unlock()
	require.False(t, reusable(), "a part with a writer isn't reused")
}
