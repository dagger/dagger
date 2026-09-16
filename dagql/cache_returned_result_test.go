package dagql

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

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
