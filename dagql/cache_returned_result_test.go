package dagql

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/engine"
	"gotest.tools/v3/assert"
)

type cacheTestCountedDeps struct {
	cacheTestOwnedDepsInt
	attachments atomic.Int32
}

func (v *cacheTestCountedDeps) AttachDependencyResults(ctx context.Context, parent AnyResult, attach func(AnyResult) (AnyResult, error)) ([]AnyResult, error) {
	v.attachments.Add(1)
	return v.cacheTestOwnedDepsInt.AttachDependencyResults(ctx, parent, attach)
}

type cacheTestEmbeddedOutput struct {
	cacheTestOwnedDepsInt
	releases atomic.Int32
}

func (v *cacheTestEmbeddedOutput) OnRelease(context.Context) error {
	v.releases.Add(1)
	return nil
}

func TestCacheEmbeddedOutputLifecycle(t *testing.T) {
	for _, depth := range []int{1, 2} {
		t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
			ctx := cacheTestContext(t.Context())
			c, err := NewCache(ctx, "", nil, nil)
			assert.NilError(t, err)
			ctx = ContextWithCache(ctx, c)
			defer cacheTestReleaseSession(t, c, ctx)
			childCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
				SessionID: "child-session", ClientID: "child-client",
			})
			defer cacheTestReleaseSession(t, c, childCtx)
			unboundCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
				SessionID: "unbound-session", ClientID: "unbound-client",
			})
			defer cacheTestReleaseSession(t, c, unboundCtx)

			handle := cacheTestVolatileSessionResourceHandle("embedded-output-input")
			assert.NilError(t, c.BindSessionResource(ctx, "test-session", "dagql-test-client", handle, "producer backing"))
			assert.NilError(t, c.BindSessionResource(childCtx, "child-session", "child-client", handle, "child backing"))
			var inputReleases atomic.Int32
			input := cacheTestDetachedResult(cacheTestIntCall("input"), cacheTestOnReleaseInt{
				Int: NewInt(42),
				onRelease: func(context.Context) error {
					inputReleases.Add(1)
					return nil
				},
			})
			input, err = input.WithSessionResourceHandle(ctx, handle)
			assert.NilError(t, err)
			attachedInput, err := c.AttachResult(ctx, "test-session", noopTypeResolver{}, input)
			assert.NilError(t, err)
			inputID := attachedInput.cacheSharedResult().id

			// Each producer owns its embedded output, but the output's recipe
			// must retain only the input, not its materialized producer(s).
			calls := make([]*ResultCall, depth+1)
			calls[0] = cacheTestIntCall("producer")
			calls[0].Receiver = &ResultCallRef{ResultID: uint64(inputID)}
			for i := 1; i <= depth; i++ {
				calls[i] = EmbeddedFieldCall(calls[i-1], "output", NewInt(0).Type())
			}
			values := make([]*cacheTestEmbeddedOutput, len(calls))
			var detached AnyResult
			for i := depth; i >= 0; i-- {
				values[i] = &cacheTestEmbeddedOutput{cacheTestOwnedDepsInt: cacheTestOwnedDepsInt{Int: NewInt(i + 1)}}
				if detached != nil {
					values[i].ownedResults = []AnyResult{detached}
				}
				detached = cacheTestDetachedResult(calls[i], values[i])
			}
			before, err := values[depth-1].ownedResults[0].RecipeID(ctx)
			assert.NilError(t, err)
			parent, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall: calls[0],
			}, ValueFunc(detached))
			assert.NilError(t, err)
			ids := []sharedResultID{parent.cacheSharedResult().id}
			for i := 0; i < depth; i++ {
				ids = append(ids, values[i].ownedResults[0].cacheSharedResult().id)
			}
			func() {
				c.egraphMu.RLock()
				defer c.egraphMu.RUnlock()
				for i, id := range ids {
					res := c.resultsByID[id]
					if res == nil {
						t.Fatalf("projection %d result %d is missing from the cache", i, id)
					}
					_, ownsInput := res.deps[inputID]
					assert.Assert(t, ownsInput, "projection %d must retain its recipe input", i)
					assert.Assert(t, cacheTestSessionResourceSetContains(res.requiredSessionResources, handle))
					for _, ancestorID := range ids[:i] {
						_, ownsAncestor := res.deps[ancestorID]
						assert.Assert(t, !ownsAncestor, "projection must not own its producer")
					}
					if i < depth {
						_, ownsChild := res.deps[ids[i+1]]
						assert.Assert(t, ownsChild, "producer must own its embedded output")
					}
				}
			}()

			child, err := c.GetOrInitCall(childCtx, "child-session", noopTypeResolver{}, &CallRequest{
				ResultCall: calls[depth],
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("embedded output must be reused, not recomputed")
			})
			assert.NilError(t, err)
			assert.Assert(t, child.HitCache())
			assert.Equal(t, child.cacheSharedResult().id, ids[depth])
			cacheTestReleaseSession(t, c, ctx)

			func() {
				c.egraphMu.RLock()
				defer c.egraphMu.RUnlock()
				assert.Equal(t, len(c.resultsByID), 2, "only output and input should survive")
				for i, id := range ids[:depth] {
					assert.Assert(t, c.resultsByID[id] == nil, "producer %d must be reclaimed", i)
					assert.Equal(t, values[i].releases.Load(), int32(1))
				}
				assert.Assert(t, c.resultsByID[ids[depth]] != nil)
				assert.Assert(t, c.resultsByID[inputID] != nil)
			}()
			assert.Equal(t, values[depth].releases.Load(), int32(0))
			assert.Equal(t, inputReleases.Load(), int32(0))
			value, ok := UnwrapAs[*cacheTestEmbeddedOutput](child)
			assert.Assert(t, ok)
			assert.Equal(t, int(value.Int), depth+1)
			after, err := child.RecipeID(childCtx)
			assert.NilError(t, err)
			assert.Equal(t, before.Digest(), after.Digest(), "reclaiming producers must preserve the output recipe")
			reloaded, hit, err := c.lookupCallRequest(childCtx, "child-session", noopTypeResolver{}, &CallRequest{ResultCall: calls[depth]})
			assert.NilError(t, err)
			assert.Assert(t, hit, "output recipe must remain reusable without materialized producers")
			assert.Equal(t, reloaded.cacheSharedResult().id, ids[depth])
			backing, err := c.ResolveSessionResource(childCtx, "child-session", "child-client", handle)
			assert.NilError(t, err)
			assert.Equal(t, backing, "child backing")

			// Resource gating must survive producer reclamation, not merely be
			// present on the producer while it is still materialized.
			_, err = c.LoadResultByResultID(unboundCtx, "unbound-session", cacheTestServer(t), uint64(ids[depth]))
			assert.ErrorContains(t, err, "has not bound the session resources this result requires")
			_, hit, err = c.lookupCallRequest(unboundCtx, "unbound-session", noopTypeResolver{}, &CallRequest{ResultCall: calls[depth]})
			assert.NilError(t, err)
			assert.Assert(t, !hit, "unbound session must not reuse the embedded output")

			cacheTestReleaseSession(t, c, childCtx)
			func() {
				c.egraphMu.RLock()
				defer c.egraphMu.RUnlock()
				assert.Equal(t, len(c.resultsByID), 0, "releasing the child must reclaim its full closure")
			}()
			cacheTestReleaseSession(t, c, ctx)
			cacheTestReleaseSession(t, c, childCtx)
			for _, value := range values {
				assert.Equal(t, value.releases.Load(), int32(1))
			}
			assert.Equal(t, inputReleases.Load(), int32(1))
		})
	}
}

func TestCacheConcurrentReturnedResult(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	defer cacheTestReleaseSession(t, c, ctx)
	call := cacheTestIntCall("returned-parent")
	value := &cacheTestCountedDeps{cacheTestOwnedDepsInt: cacheTestOwnedDepsInt{
		Int:          NewInt(1),
		ownedResults: []AnyResult{cacheTestIntResult(cacheTestIntCall("returned-child"), 2)},
	}}
	parent, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestDetachedResult(call, value), nil
	})
	assert.NilError(t, err)
	// Different calls return the same published result while lookups inspect its
	// expiry. Adoption must preserve the attached object and its dependencies.
	const aliases = 32
	errs := make(chan error, aliases+1)
	start := make(chan struct{})
	for i := range aliases {
		go func() {
			<-start
			result, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall(fmt.Sprintf("return-alias-%d", i))}, func(context.Context) (AnyResult, error) { return parent, nil })
			if err == nil && result.cacheSharedResult() != parent.cacheSharedResult() {
				err = fmt.Errorf("alias did not retain the published result")
			}
			errs <- err
		}()
	}
	go func() {
		<-start
		for range aliases {
			_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("published parent was lost")
			})
			if err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	close(start)
	for range aliases + 1 {
		assert.NilError(t, <-errs)
	}
	assert.Equal(t, value.attachments.Load(), int32(1), "published dependencies must not be attached again")
	assert.Equal(t, len(value.ownedResults), 1)
	assert.Equal(t, cacheTestUnwrapInt(t, value.ownedResults[0]), 2)
}
