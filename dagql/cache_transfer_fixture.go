package dagql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/dagger/dagger/dagql/call"
)

// WithTransferFixtureRoots lends exact registered rows to the gated test
// fixture. It neither decodes values nor creates lasting session ownership.
func (c *Cache) WithTransferFixtureRoots(ctx context.Context, sessionID string, ids []*call.ID, consume func([]AnyResult) error) (rerr error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer func() {
		if op.finish(rerr == nil) {
			rerr = errors.Join(rerr, ErrCacheSessionReleased)
		}
	}()
	rows := make([]*sharedResult, 0, len(ids))
	gens := make([]uint64, 0, len(ids))
	defer func() {
		releaseCtx := context.WithoutCancel(ctx)
		c.egraphMu.Lock()
		var queue collectionQueue
		for _, row := range rows {
			q, err := c.decrementIncomingOwnershipLocked(releaseCtx, row, nil)
			queue = append(queue, q...)
			rerr = errors.Join(rerr, err)
		}
		releases, err := c.collectUnownedResultsLocked(releaseCtx, queue)
		c.egraphMu.Unlock()
		rerr = errors.Join(rerr, err, runOnReleaseFuncs(releaseCtx, releases))
	}()
	err = func() error {
		c.egraphMu.Lock()
		defer c.egraphMu.Unlock()
		for _, id := range ids {
			row, err := c.transferFixtureRowLocked(sessionID, id)
			if err != nil {
				return err
			}
			c.incrementIncomingOwnershipLocked(ctx, row)
			rows = append(rows, row)
			gens = append(gens, row.requiredSessionResourcesGen.Load())
		}
		return context.Cause(ctx)
	}()
	if err != nil {
		return err
	}
	roots := make([]AnyResult, len(rows))
	for i, row := range rows {
		if !c.sessionStillSatisfiesResourceRequirements(sessionID, row, gens[i]) {
			return fmt.Errorf("fixture root %d: session resource requirements changed", row.id)
		}
		roots[i] = Result[Typed]{shared: row}
	}
	if consume == nil {
		return fmt.Errorf("missing fixture consumer")
	}
	return consume(roots)
}

func (c *Cache) transferFixtureRowLocked(sessionID string, id *call.ID) (*sharedResult, error) {
	if id == nil || !id.IsHandle() || id.EngineResultID() == 0 {
		return nil, fmt.Errorf("fixture requires exact result handles")
	}
	row := c.resultsByID[sharedResultID(id.EngineResultID())]
	if row == nil {
		return nil, fmt.Errorf("fixture result %d is missing", id.EngineResultID())
	}
	frame := row.loadResultCall()
	if frame == nil || frame.Type == nil || !reflect.DeepEqual(frame.Type.toAST(), id.Type().ToAST()) {
		return nil, fmt.Errorf("fixture handle type mismatch")
	}
	if !c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) {
		return nil, fmt.Errorf("fixture result %d requires session resources", row.id)
	}
	return row, nil
}

type TransferFixtureRow struct {
	ResultID      uint64               `json:"resultID"`
	Call          *ResultCall          `json:"call"`
	Imported      bool                 `json:"imported"`
	Persisted     bool                 `json:"persisted"`
	DependencyIDs []uint64             `json:"dependencyIDs"`
	Offers        []PersistedPartOffer `json:"offers"`
	OutputClasses []uint64             `json:"outputClasses"`
	TermIDs       []uint64             `json:"termIDs"`
}
type TransferFixtureReport struct {
	Rows   []TransferFixtureRow   `json:"rows"`
	Owners []CacheDebugOfferOwner `json:"owners"`
}

// TransferFixtureSnapshot copies raw metadata without demanding values or
// retaining rows. Filesystem counters are read separately by the fixture.
func (c *Cache) TransferFixtureSnapshot(ctx context.Context, sessionID string, ids []*call.ID) (report TransferFixtureReport, rerr error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return report, err
	}
	defer func() {
		if op.finish(rerr == nil) {
			rerr = errors.Join(rerr, ErrCacheSessionReleased)
		}
	}()
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	selected := map[sharedResultID]bool{}
	for _, id := range ids {
		row, err := c.transferFixtureRowLocked(sessionID, id)
		if err != nil {
			return report, err
		}
		selected[row.id] = true
	}
	for id, row := range c.resultsByID {
		if len(ids) > 0 && !selected[id] {
			continue
		}
		if !c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) {
			continue
		}
		frame := row.loadResultCall()
		if frame == nil {
			continue
		}
		offers, err := row.pendingOffersLocked()
		if err != nil {
			return report, err
		}
		entry := TransferFixtureRow{ResultID: uint64(id), Call: frame.clone(), Imported: row.imported, Offers: offers}
		_, entry.Persisted = c.persistedEdgesByResult[id]
		for dep := range row.deps {
			entry.DependencyIDs = append(entry.DependencyIDs, uint64(dep))
		}
		for eq := range c.outputEqClassesForResultLocked(id) {
			entry.OutputClasses = append(entry.OutputClasses, uint64(eq))
		}
		for term := range c.resultTerms[id] {
			entry.TermIDs = append(entry.TermIDs, uint64(term))
		}
		slices.Sort(entry.TermIDs)
		slices.Sort(entry.DependencyIDs)
		slices.Sort(entry.OutputClasses)
		report.Rows = append(report.Rows, entry)
	}
	for _, owner := range c.debugOfferOwnersLocked() {
		if len(ids) == 0 || selected[sharedResultID(owner.OriginResultID)] {
			report.Owners = append(report.Owners, owner)
		}
	}
	slices.SortFunc(report.Rows, func(a, b TransferFixtureRow) int {
		if a.ResultID < b.ResultID {
			return -1
		}
		if a.ResultID > b.ResultID {
			return 1
		}
		return 0
	})
	return report, context.Cause(ctx)
}
