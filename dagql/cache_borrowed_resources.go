package dagql

import (
	"context"
	"fmt"
	"sync"
)

// borrowedResultsKey carries the borrowedResults of the call whose resolver
// runs under a context.
type borrowedResultsKey struct{}

// borrowedResults are the results through which a call's resolver borrowed
// session resources that its call's inputs don't name
// (RequireSessionResourcesOf).
type borrowedResults struct {
	mu      sync.Mutex
	results []*sharedResult
}

func (b *borrowedResults) load() []*sharedResult {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.results
}

// RequireSessionResourcesOf makes the result of the call whose resolver runs
// under ctx require the session resources that each of results requires, as
// if the result depended on them: another session is served the result only
// once it has loaded the same secrets and sockets, as for any result built
// on them.
//
// A resolver calls it when the work it ran used a session's resources that
// its call's key can't name, because they were reached during the work and
// aren't among the call's inputs: credentials the session lends to code the
// call runs, for one. The key then stays free of the session, so a session
// holding the same resources is served the result, and one without them
// computes its own.
//
// The result gets a dependency on each result in the closures of results
// that is itself a session resource, so only the resources travel with it.
// Results that require none add nothing. Like DeclinePersistence, it must
// be called with the resolver's own context, and does nothing outside a
// resolver the cache runs. Each result must be attached to the cache.
func RequireSessionResourcesOf(ctx context.Context, results ...AnyResult) error {
	b, _ := ctx.Value(borrowedResultsKey{}).(*borrowedResults)
	if b == nil {
		return nil
	}
	shareds := make([]*sharedResult, 0, len(results))
	for _, res := range results {
		if res == nil {
			continue
		}
		shared := res.cacheSharedResult()
		if shared == nil || shared.id == 0 {
			return fmt.Errorf("require session resources of %T: not an attached result", res)
		}
		shareds = append(shareds, shared)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.results = append(b.results, shareds...)
	return nil
}

// requireBorrowedSessionResourcesLocked gives res a dependency on every
// session-resource result in the closures of the results its call's
// resolver borrowed through, so res requires what they require. Callers hold
// the egraphMu write lock.
func (c *Cache) requireBorrowedSessionResourcesLocked(ctx context.Context, res *sharedResult, borrowed []*sharedResult) error {
	var resources []*sharedResult
	seen := map[sharedResultID]bool{}
	var walk func(*sharedResult)
	walk = func(r *sharedResult) {
		if seen[r.id] {
			return
		}
		seen[r.id] = true
		if r.requiredSessionResources == nil || r.requiredSessionResources.Empty() {
			return
		}
		if r.sessionResourceHandle != "" {
			resources = append(resources, r)
		}
		for _, depID := range r.deps {
			if dep := c.resultsByID[depID]; dep != nil {
				walk(dep)
			}
		}
	}
	for _, b := range borrowed {
		// A borrowed result the cache no longer holds can't say what it
		// required: fail rather than publish a result without it.
		if c.resultsByID[b.id] != b {
			return fmt.Errorf("require borrowed session resources: result %d was already collected", b.id)
		}
		walk(b)
	}
	return c.addExplicitDependenciesLocked(ctx, res, resources, "borrowed_session_resource")
}
