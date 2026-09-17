package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// Controls of the environment-gated test fixture that act on the cache. They
// are internal and test-only by convention: the gate is what keeps them out
// of a production schema, not these helpers. Each uses the ordinary session
// resource checks, counted operations and exact handle validation, and none
// can alter an output's finality, a match or an owner-synchronization result.

// fixtureHolds are the fixture's outstanding hold tokens. A token owns one
// ordinary incoming hold on each of its rows. Tokens live in the cache's
// memory only, so none survives a restart, and close releases whatever a
// test left behind.
type fixtureHolds struct {
	mu     sync.Mutex
	tokens map[string][]*sharedResult
}

// TransferFixtureHold is the result of a fixture hold.
type TransferFixtureHold struct {
	Token     string   `json:"token"`
	ResultIDs []uint64 `json:"resultIDs"`
}

// HoldTransferFixtureRoots takes one incoming hold on each exact row, after
// the session's resource checks, and returns the token that owns them.
func (c *Cache) HoldTransferFixtureRoots(ctx context.Context, sessionID string, ids []*call.ID) (_ TransferFixtureHold, rerr error) {
	if len(ids) == 0 {
		return TransferFixtureHold{}, fmt.Errorf("fixture hold requires exact result handles")
	}
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return TransferFixtureHold{}, err
	}
	defer func() {
		if op.finish(rerr == nil) {
			rerr = errors.Join(rerr, ErrCacheSessionReleased)
		}
	}()
	hold := TransferFixtureHold{Token: identity.NewID()}
	rows := make([]*sharedResult, 0, len(ids))
	c.egraphMu.Lock()
	for _, id := range ids {
		row, err := c.transferFixtureRowLocked(sessionID, id)
		if err != nil {
			c.egraphMu.Unlock()
			return TransferFixtureHold{}, err
		}
		rows = append(rows, row)
	}
	// Every handle is valid: only now does anything change.
	for _, row := range rows {
		c.incrementIncomingOwnershipLocked(ctx, row)
		hold.ResultIDs = append(hold.ResultIDs, uint64(row.id))
	}
	c.egraphMu.Unlock()

	c.fixtureHolds.mu.Lock()
	if c.fixtureHolds.tokens == nil {
		c.fixtureHolds.tokens = map[string][]*sharedResult{}
	}
	c.fixtureHolds.tokens[hold.Token] = rows
	c.fixtureHolds.mu.Unlock()
	return hold, nil
}

// ReleaseTransferFixtureHold ends a token's holds through the ordinary
// release path. A token is released once; an unknown token is an error.
func (c *Cache) ReleaseTransferFixtureHold(ctx context.Context, token string) (TransferFixtureHold, error) {
	c.fixtureHolds.mu.Lock()
	rows, ok := c.fixtureHolds.tokens[token]
	delete(c.fixtureHolds.tokens, token)
	c.fixtureHolds.mu.Unlock()
	if !ok {
		return TransferFixtureHold{}, fmt.Errorf("unknown fixture hold token")
	}
	released := TransferFixtureHold{Token: token}
	for _, row := range rows {
		released.ResultIDs = append(released.ResultIDs, uint64(row.id))
	}
	return released, c.releaseFixtureRows(ctx, rows)
}

// TransferFixtureHoldCount reports how many hold tokens are outstanding.
func (c *Cache) TransferFixtureHoldCount() int {
	c.fixtureHolds.mu.Lock()
	defer c.fixtureHolds.mu.Unlock()
	return len(c.fixtureHolds.tokens)
}

// releaseAllFixtureHolds runs at close with an uncanceled context.
func (c *Cache) releaseAllFixtureHolds(ctx context.Context) error {
	c.fixtureHolds.mu.Lock()
	tokens := c.fixtureHolds.tokens
	c.fixtureHolds.tokens = nil
	c.fixtureHolds.mu.Unlock()
	var err error
	for _, rows := range tokens {
		err = errors.Join(err, c.releaseFixtureRows(ctx, rows))
	}
	return err
}

func (c *Cache) releaseFixtureRows(ctx context.Context, rows []*sharedResult) error {
	releaseCtx := context.WithoutCancel(ctx)
	var rerr error
	c.egraphMu.Lock()
	var queue collectionQueue
	for _, row := range rows {
		q, err := c.decrementIncomingOwnershipLocked(releaseCtx, row, nil)
		queue = append(queue, q...)
		rerr = errors.Join(rerr, err)
	}
	releases, err := c.collectUnownedResultsLocked(releaseCtx, queue)
	c.egraphMu.Unlock()
	return errors.Join(rerr, err, runOnReleaseFuncs(releaseCtx, releases))
}

// TransferFixtureDroppedRoot reports one row's retention edge.
type TransferFixtureDroppedRoot struct {
	ResultID uint64 `json:"resultID"`
	// Removed is false when the row had no removable saved edge: none at all,
	// or an unpruneable one, which this control leaves alone.
	Removed bool `json:"removed"`
	// Registered reports whether the row is still registered afterwards. It
	// stays registered while any other owner holds it.
	Registered bool `json:"registered"`
}

// DropTransferFixtureRetainedRoots removes the saved retention edge of each
// exact row through the ordinary removal path. It removes retention, not rows,
// snapshots or dependencies, and says nothing about automatic prune policy.
func (c *Cache) DropTransferFixtureRetainedRoots(ctx context.Context, sessionID string, ids []*call.ID) (_ []TransferFixtureDroppedRoot, rerr error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("fixture dropRetainedRoots requires exact result handles")
	}
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if op.finish(rerr == nil) {
			rerr = errors.Join(rerr, ErrCacheSessionReleased)
		}
	}()
	rowIDs := make([]sharedResultID, 0, len(ids))
	c.egraphMu.Lock()
	for _, id := range ids {
		row, err := c.transferFixtureRowLocked(sessionID, id)
		if err != nil {
			c.egraphMu.Unlock()
			return nil, err
		}
		rowIDs = append(rowIDs, row.id)
	}
	c.egraphMu.Unlock()
	slices.Sort(rowIDs)
	rowIDs = slices.Compact(rowIDs)

	out := make([]TransferFixtureDroppedRoot, 0, len(rowIDs))
	for _, id := range rowIDs {
		removed, err := c.removePersistedEdge(ctx, id)
		rerr = errors.Join(rerr, err)
		c.egraphMu.RLock()
		_, registered := c.resultsByID[id]
		c.egraphMu.RUnlock()
		out = append(out, TransferFixtureDroppedRoot{ResultID: uint64(id), Removed: removed, Registered: registered})
	}
	return out, rerr
}
