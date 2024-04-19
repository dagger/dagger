package solver

import (
	"context"
	_ "crypto/sha256"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func init() {
	rand.Seed(time.Now().UnixNano()) //nolint:staticcheck // Ignore SA1019. No way to get the automatically generated seed since Go 1.20.
	if debugScheduler {
		logrus.SetOutput(os.Stdout)
		logrus.SetLevel(logrus.DebugLevel)
	}
}

func TestSingleLevelActiveGraph(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	j0, err := s.NewJob("job0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:  "v0",
			value: "result0",
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, *g0.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g0.Vertex.(*vertex).execCallCount, int64(1))

	// calling again with same digest just uses the active queue
	j1, err := s.NewJob("job1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:  "v0",
			value: "result1",
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, *g0.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g0.Vertex.(*vertex).execCallCount, int64(1))
	require.Equal(t, *g1.Vertex.(*vertex).cacheCallCount, int64(0))
	require.Equal(t, *g1.Vertex.(*vertex).execCallCount, int64(0))

	require.NoError(t, j0.Discard())
	j0 = nil

	// after discarding j0, j1 still holds the state

	j2, err := s.NewJob("job2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:  "v0",
			value: "result2",
		}),
	}
	g2.Vertex.(*vertex).setupCallCounters()

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, *g0.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g0.Vertex.(*vertex).execCallCount, int64(1))
	require.Equal(t, *g1.Vertex.(*vertex).cacheCallCount, int64(0))
	require.Equal(t, *g1.Vertex.(*vertex).execCallCount, int64(0))
	require.Equal(t, *g2.Vertex.(*vertex).cacheCallCount, int64(0))
	require.Equal(t, *g2.Vertex.(*vertex).execCallCount, int64(0))

	require.NoError(t, j1.Discard())
	j1 = nil
	require.NoError(t, j2.Discard())
	j2 = nil

	// everything should be released now

	j3, err := s.NewJob("job3")
	require.NoError(t, err)

	defer func() {
		if j3 != nil {
			j3.Discard()
		}
	}()

	g3 := Edge{
		Vertex: vtx(vtxOpt{
			name:  "v0",
			value: "result3",
		}),
	}
	g3.Vertex.(*vertex).setupCallCounters()

	res, err = j3.Build(ctx, g3)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result3")

	require.Equal(t, *g3.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g3.Vertex.(*vertex).execCallCount, int64(1))

	require.NoError(t, j3.Discard())
	j3 = nil

	// repeat the same test but make sure the build run in parallel now

	j4, err := s.NewJob("job4")
	require.NoError(t, err)

	defer func() {
		if j4 != nil {
			j4.Discard()
		}
	}()

	j5, err := s.NewJob("job5")
	require.NoError(t, err)

	defer func() {
		if j5 != nil {
			j5.Discard()
		}
	}()

	g4 := Edge{
		Vertex: vtx(vtxOpt{
			name:       "v0",
			cacheDelay: 100 * time.Millisecond,
			value:      "result4",
		}),
	}
	g4.Vertex.(*vertex).setupCallCounters()

	eg, _ := errgroup.WithContext(ctx)

	eg.Go(func() error {
		res, err := j4.Build(ctx, g4)
		require.NoError(t, err)
		require.Equal(t, unwrap(res), "result4")
		return err
	})

	eg.Go(func() error {
		res, err := j5.Build(ctx, g4)
		require.NoError(t, err)
		require.Equal(t, unwrap(res), "result4")
		return err
	})

	require.NoError(t, eg.Wait())

	require.Equal(t, *g4.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g4.Vertex.(*vertex).execCallCount, int64(1))
}

func TestSingleLevelCache(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	j0, err := s.NewJob("job0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j0.Discard())
	j0 = nil

	// first try that there is no match for different cache
	j1, err := s.NewJob("job1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v1",
			cacheKeySeed: "seed1",
			value:        "result1",
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result1")

	require.Equal(t, *g1.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g1.Vertex.(*vertex).execCallCount, int64(1))

	require.NoError(t, j1.Discard())
	j1 = nil

	// expect cache match for first build

	j2, err := s.NewJob("job2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v2",
			cacheKeySeed: "seed0", // same as first build
			value:        "result2",
		}),
	}
	g2.Vertex.(*vertex).setupCallCounters()

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, *g0.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g0.Vertex.(*vertex).execCallCount, int64(1))
	require.Equal(t, *g2.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g2.Vertex.(*vertex).execCallCount, int64(0))

	require.NoError(t, j2.Discard())
	j2 = nil
}

func TestSingleLevelCacheParallel(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	// rebuild in parallel. only executed once.

	j0, err := s.NewJob("job0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	wait2Ready := blockingFuncion(2)

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			cachePreFunc: wait2Ready,
			value:        "result0",
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	j1, err := s.NewJob("job1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v1",
			cacheKeySeed: "seed0", // same as g0
			cachePreFunc: wait2Ready,
			value:        "result0",
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	eg, _ := errgroup.WithContext(ctx)

	eg.Go(func() error {
		res, err := j0.Build(ctx, g0)
		require.NoError(t, err)
		require.Equal(t, unwrap(res), "result0")
		return err
	})

	eg.Go(func() error {
		res, err := j1.Build(ctx, g1)
		require.NoError(t, err)
		require.Equal(t, unwrap(res), "result0")
		return err
	})

	require.NoError(t, eg.Wait())

	require.Equal(t, int64(1), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(1), *g1.Vertex.(*vertex).cacheCallCount)
	// only one execution ran
	require.Equal(t, int64(1), *g0.Vertex.(*vertex).execCallCount+*g1.Vertex.(*vertex).execCallCount)
}

func TestMultiLevelCacheParallel(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	// rebuild in parallel. only executed once.

	j0, err := s.NewJob("job0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	wait2Ready := blockingFuncion(2)
	wait2Ready2 := blockingFuncion(2)

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			cachePreFunc: wait2Ready,
			value:        "result0",
			inputs: []Edge{{
				Vertex: vtx(vtxOpt{
					name:         "v0-c0",
					cacheKeySeed: "seed0-c0",
					cachePreFunc: wait2Ready2,
					value:        "result0-c0",
				})},
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	j1, err := s.NewJob("job1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v1",
			cacheKeySeed: "seed0", // same as g0
			cachePreFunc: wait2Ready,
			value:        "result0",
			inputs: []Edge{{
				Vertex: vtx(vtxOpt{
					name:         "v1-c0",
					cacheKeySeed: "seed0-c0", // same as g0
					cachePreFunc: wait2Ready2,
					value:        "result0-c",
				})},
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	eg, _ := errgroup.WithContext(ctx)

	eg.Go(func() error {
		res, err := j0.Build(ctx, g0)
		require.NoError(t, err)
		require.Equal(t, unwrap(res), "result0")
		return err
	})

	eg.Go(func() error {
		res, err := j1.Build(ctx, g1)
		require.NoError(t, err)
		require.Equal(t, unwrap(res), "result0")
		return err
	})

	require.NoError(t, eg.Wait())

	require.Equal(t, int64(2), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g1.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g0.Vertex.(*vertex).execCallCount+*g1.Vertex.(*vertex).execCallCount)
}

func TestSingleCancelCache(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	j0, err := s.NewJob("job0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	ctx, cancel := context.WithCancelCause(ctx)

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name: "v0",
			cachePreFunc: func(ctx context.Context) error {
				cancel(errors.WithStack(context.Canceled))
				<-ctx.Done()
				return nil // error should still come from context
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	_, err = j0.Build(ctx, g0)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, context.Canceled))

	require.Equal(t, *g0.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g0.Vertex.(*vertex).execCallCount, int64(0))

	require.NoError(t, j0.Discard())
	j0 = nil
}
func TestSingleCancelExec(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	j1, err := s.NewJob("job1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	ctx, cancel := context.WithCancelCause(ctx)

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name: "v2",
			execPreFunc: func(ctx context.Context) error {
				cancel(errors.WithStack(context.Canceled))
				<-ctx.Done()
				return nil // error should still come from context
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	_, err = j1.Build(ctx, g1)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, context.Canceled))

	require.Equal(t, *g1.Vertex.(*vertex).cacheCallCount, int64(1))
	require.Equal(t, *g1.Vertex.(*vertex).execCallCount, int64(1))

	require.NoError(t, j1.Discard())
	j1 = nil
}

func TestSingleCancelParallel(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	// run 2 in parallel cancel first, second one continues without errors
	eg, ctx := errgroup.WithContext(ctx)

	firstReady := make(chan struct{})
	firstErrored := make(chan struct{})

	eg.Go(func() error {
		j, err := s.NewJob("job2")
		require.NoError(t, err)

		defer func() {
			if j != nil {
				j.Discard()
			}
		}()

		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(errors.WithStack(context.Canceled))

		g := Edge{
			Vertex: vtx(vtxOpt{
				name:  "v2",
				value: "result2",
				cachePreFunc: func(ctx context.Context) error {
					close(firstReady)
					time.Sleep(200 * time.Millisecond)
					cancel(errors.WithStack(context.Canceled))
					<-firstErrored
					return nil
				},
			}),
		}

		_, err = j.Build(ctx, g)
		close(firstErrored)
		require.Error(t, err)
		require.Equal(t, true, errors.Is(err, context.Canceled))
		return nil
	})

	eg.Go(func() error {
		j, err := s.NewJob("job3")
		require.NoError(t, err)

		defer func() {
			if j != nil {
				j.Discard()
			}
		}()

		g := Edge{
			Vertex: vtx(vtxOpt{
				name: "v2",
			}),
		}
		<-firstReady

		res, err := j.Build(ctx, g)
		require.NoError(t, err)
		require.Equal(t, unwrap(res), "result2")
		return err
	})

	require.NoError(t, eg.Wait())
}

func TestMultiLevelCalculation(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g := Edge{
		Vertex: vtxSum(1, vtxOpt{
			inputs: []Edge{
				{Vertex: vtxSum(0, vtxOpt{
					inputs: []Edge{
						{Vertex: vtxConst(7, vtxOpt{})},
						{Vertex: vtxConst(2, vtxOpt{})},
					},
				})},
				{Vertex: vtxSum(0, vtxOpt{
					inputs: []Edge{
						{Vertex: vtxConst(7, vtxOpt{})},
						{Vertex: vtxConst(2, vtxOpt{})},
					},
				})},
				{Vertex: vtxConst(2, vtxOpt{})},
				{Vertex: vtxConst(2, vtxOpt{})},
				{Vertex: vtxConst(19, vtxOpt{})},
			},
		}),
	}

	res, err := j0.Build(ctx, g)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 42) // 1 + 2*(7 + 2) + 2 + 2 + 19

	require.NoError(t, j0.Discard())
	j0 = nil

	// repeating same build with cache should behave the same
	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtxSum(1, vtxOpt{
			inputs: []Edge{
				{Vertex: vtxSum(0, vtxOpt{
					inputs: []Edge{
						{Vertex: vtxConst(7, vtxOpt{})},
						{Vertex: vtxConst(2, vtxOpt{})},
					},
				})},
				{Vertex: vtxSum(0, vtxOpt{
					inputs: []Edge{
						{Vertex: vtxConst(7, vtxOpt{})},
						{Vertex: vtxConst(2, vtxOpt{})},
					},
				})},
				{Vertex: vtxConst(2, vtxOpt{})},
				{Vertex: vtxConst(2, vtxOpt{})},
				{Vertex: vtxConst(19, vtxOpt{})},
			},
		}),
	}
	res, err = j1.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 42)
}

func TestHugeGraph(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	nodes := 1000

	g, v := generateSubGraph(nodes)
	// printGraph(g, "")
	g.Vertex.(*vertexSum).setupCallCounters()

	res, err := j0.Build(ctx, g)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), v)
	require.Equal(t, int64(nodes), *g.Vertex.(*vertexSum).cacheCallCount)
	// execCount := *g.Vertex.(*vertexSum).execCallCount
	// require.True(t, execCount < 1000)
	// require.True(t, execCount > 600)
	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g.Vertex.(*vertexSum).setupCallCounters()
	res, err = j1.Build(ctx, g)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), v)

	require.Equal(t, int64(nodes), *g.Vertex.(*vertexSum).cacheCallCount)
	require.Equal(t, int64(0), *g.Vertex.(*vertexSum).execCallCount)
	require.Equal(t, int64(1), cacheManager.loadCounter)
}

// TestOptimizedCacheAccess tests that inputs are not loaded from cache unless
// they are really needed
func TestOptimizedCacheAccess(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2",
					cacheKeySeed: "seed2",
					value:        "result2",
				})},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				1: digestFromResult,
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(3), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(3), *g0.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	// changing cache seed for the input with slow cache should not pull result1

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-nocache",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1-nocache",
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2-changed",
					cacheKeySeed: "seed2-changed",
					value:        "result2", // produces same slow key as g0
				})},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				1: digestFromResult,
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(3), *g1.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(1), *g1.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(1), cacheManager.loadCounter)

	require.NoError(t, j1.Discard())
	j1 = nil
}

// TestOptimizedCacheAccess2 is a more narrow case that tests that inputs are
// not loaded from cache unless they are really needed. Inputs that match by
// definition should be less prioritized for slow cache calculation than the
// inputs that didn't.
func TestOptimizedCacheAccess2(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2",
					cacheKeySeed: "seed2",
					value:        "result2",
				})},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
				1: digestFromResult,
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(3), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(3), *g0.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	// changing cache seed for the input with slow cache should not pull result1

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-nocache",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2-changed",
					cacheKeySeed: "seed2-changed",
					value:        "result2", // produces same slow key as g0
				})},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
				1: digestFromResult,
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(3), *g1.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(1), *g1.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(1), cacheManager.loadCounter) // v1 is never loaded nor executed

	require.NoError(t, j1.Discard())
	j1 = nil

	// make sure that both inputs are still used for slow cache hit
	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-nocache",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1-changed2",
					value:        "result1",
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2-changed",
					cacheKeySeed: "seed2-changed2",
					value:        "result2", // produces same slow key as g0
				})},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
				1: digestFromResult,
			},
		}),
	}
	g2.Vertex.(*vertex).setupCallCounters()

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(3), *g2.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g2.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(2), cacheManager.loadCounter)

	require.NoError(t, j2.Discard())
	j1 = nil
}

func TestSlowCache(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
		}),
	}

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j0.Discard())
	j0 = nil

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v2",
			cacheKeySeed: "seed0",
			value:        "not-cached",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v3",
					cacheKeySeed: "seed3",
					value:        "result1", // used for slow key
				})},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
		}),
	}

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j1.Discard())
	j1 = nil
}

// TestParallelInputs validates that inputs are processed in parallel
func TestParallelInputs(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	wait2Ready := blockingFuncion(2)
	wait2Ready2 := blockingFuncion(2)

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
					cachePreFunc: wait2Ready,
					execPreFunc:  wait2Ready2,
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2",
					cacheKeySeed: "seed2",
					value:        "result2",
					cachePreFunc: wait2Ready,
					execPreFunc:  wait2Ready2,
				})},
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")
	require.NoError(t, j0.Discard())
	j0 = nil

	require.Equal(t, int64(3), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(3), *g0.Vertex.(*vertex).execCallCount)
}

func TestErrorReturns(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
					cachePreFunc: func(ctx context.Context) error {
						return errors.Errorf("error-from-test")
					},
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2",
					cacheKeySeed: "seed2",
					value:        "result2",
				})},
			},
		}),
	}

	_, err = j0.Build(ctx, g0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "error-from-test")

	require.NoError(t, j0.Discard())
	j0 = nil

	// error with cancel error. to check that this isn't mixed up with regular build cancel.

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
					cachePreFunc: func(ctx context.Context) error {
						return context.Canceled
					},
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2",
					cacheKeySeed: "seed2",
					value:        "result2",
				})},
			},
		}),
	}

	_, err = j1.Build(ctx, g1)
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, context.Canceled))

	require.NoError(t, j1.Discard())
	j1 = nil

	// error from exec

	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
				{Vertex: vtx(vtxOpt{
					name:         "v2",
					cacheKeySeed: "seed3",
					value:        "result2",
					execPreFunc: func(ctx context.Context) error {
						return errors.Errorf("exec-error-from-test")
					},
				})},
			},
		}),
	}

	_, err = j2.Build(ctx, g2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exec-error-from-test")

	require.NoError(t, j2.Discard())
	j1 = nil
}

func TestMultipleCacheSources(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
			},
		}),
	}

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")
	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	cacheManager2 := newTrackingCacheManager(NewInMemoryCacheManager())

	l2 := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager2,
	})
	defer l2.Close()

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-no-cache",
			cacheSource:  cacheManager,
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1-no-cache",
					cacheSource:  cacheManager,
				})},
			},
		}),
	}

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(1), cacheManager.loadCounter)
	require.Equal(t, int64(0), cacheManager2.loadCounter)

	require.NoError(t, j1.Discard())
	j0 = nil

	// build on top of old cache
	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v2",
			cacheKeySeed: "seed2",
			value:        "result2",
			inputs:       []Edge{g1},
		}),
	}

	res, err = j1.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result2")

	require.Equal(t, int64(2), cacheManager.loadCounter)
	require.Equal(t, int64(0), cacheManager2.loadCounter)

	require.NoError(t, j1.Discard())
	j1 = nil
}

func TestRepeatBuildWithIgnoreCache(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(2), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g0.Vertex.(*vertex).execCallCount)

	require.NoError(t, j0.Discard())
	j0 = nil

	// rebuild with ignore-cache reevaluates everything

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-1",
			ignoreCache:  true,
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1-1",
					ignoreCache:  true,
				})},
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0-1")

	require.Equal(t, int64(2), *g1.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g1.Vertex.(*vertex).execCallCount)

	require.NoError(t, j1.Discard())
	j1 = nil

	// ignore-cache in child reevaluates parent

	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-2",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1-2",
					ignoreCache:  true,
				})},
			},
		}),
	}
	g2.Vertex.(*vertex).setupCallCounters()

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0-2")

	require.Equal(t, int64(2), *g2.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g2.Vertex.(*vertex).execCallCount)

	require.NoError(t, j2.Discard())
	j2 = nil
}

// TestIgnoreCacheResumeFromSlowCache tests that parent cache resumes if child
// with ignore-cache generates same slow cache key
func TestIgnoreCacheResumeFromSlowCache(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(2), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g0.Vertex.(*vertex).execCallCount)

	require.NoError(t, j0.Discard())
	j0 = nil

	// rebuild reevaluates child, but not parent

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0-1", // doesn't matter but avoid match because another bug
			cacheKeySeed: "seed0",
			value:        "result0-no-cache",
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1-1",
					cacheKeySeed: "seed1-1", // doesn't matter but avoid match because another bug
					value:        "result1", // same as g0
					ignoreCache:  true,
				})},
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(2), *g1.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(1), *g1.Vertex.(*vertex).execCallCount)

	require.NoError(t, j1.Discard())
	j1 = nil
}

func TestParallelBuildsIgnoreCache(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	// match by vertex digest
	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed1",
			value:        "result1",
			ignoreCache:  true,
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result1")

	require.NoError(t, j0.Discard())
	j0 = nil
	require.NoError(t, j1.Discard())
	j1 = nil

	// new base
	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v2",
			cacheKeySeed: "seed2",
			value:        "result2",
		}),
	}
	g2.Vertex.(*vertex).setupCallCounters()

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result2")

	// match by cache key
	j3, err := l.NewJob("j3")
	require.NoError(t, err)

	defer func() {
		if j3 != nil {
			j3.Discard()
		}
	}()

	g3 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v3",
			cacheKeySeed: "seed2",
			value:        "result3",
			ignoreCache:  true,
		}),
	}
	g3.Vertex.(*vertex).setupCallCounters()

	res, err = j3.Build(ctx, g3)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result3")

	// add another ignorecache merges now

	j4, err := l.NewJob("j4")
	require.NoError(t, err)

	defer func() {
		if j4 != nil {
			j4.Discard()
		}
	}()

	g4 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v4",
			cacheKeySeed: "seed2", // same as g2/g3
			value:        "result4",
			ignoreCache:  true,
		}),
	}
	g4.Vertex.(*vertex).setupCallCounters()

	res, err = j4.Build(ctx, g4)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result3")

	// add another !ignorecache merges now

	j5, err := l.NewJob("j5")
	require.NoError(t, err)

	defer func() {
		if j5 != nil {
			j5.Discard()
		}
	}()

	g5 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v5",
			cacheKeySeed: "seed2", // same as g2/g3/g4
			value:        "result5",
		}),
	}
	g5.Vertex.(*vertex).setupCallCounters()

	res, err = j5.Build(ctx, g5)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result3")
}

func TestSubbuild(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtxSum(1, vtxOpt{
			inputs: []Edge{
				{Vertex: vtxSubBuild(Edge{Vertex: vtxConst(7, vtxOpt{})}, vtxOpt{
					cacheKeySeed: "seed0",
				})},
			},
		}),
	}
	g0.Vertex.(*vertexSum).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 8)

	require.Equal(t, int64(2), *g0.Vertex.(*vertexSum).cacheCallCount)
	require.Equal(t, int64(2), *g0.Vertex.(*vertexSum).execCallCount)

	require.NoError(t, j0.Discard())
	j0 = nil

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g0.Vertex.(*vertexSum).setupCallCounters()

	res, err = j1.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 8)

	require.Equal(t, int64(2), *g0.Vertex.(*vertexSum).cacheCallCount)
	require.Equal(t, int64(0), *g0.Vertex.(*vertexSum).execCallCount)

	require.NoError(t, j1.Discard())
	j1 = nil
}

func TestCacheWithSelector(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
			},
			selectors: map[int]digest.Digest{
				0: dgst("sel0"),
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(2), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g0.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	// repeat, cache is matched

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-no-cache",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1-no-cache",
				})},
			},
			selectors: map[int]digest.Digest{
				0: dgst("sel0"),
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(2), *g1.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(0), *g1.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(1), cacheManager.loadCounter)

	require.NoError(t, j1.Discard())
	j1 = nil

	// using different selector doesn't match

	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-1",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1-1",
				})},
			},
			selectors: map[int]digest.Digest{
				0: dgst("sel1"),
			},
		}),
	}
	g2.Vertex.(*vertex).setupCallCounters()

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0-1")

	require.Equal(t, int64(2), *g2.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(1), *g2.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(2), cacheManager.loadCounter)

	require.NoError(t, j2.Discard())
	j2 = nil
}

func TestCacheSlowWithSelector(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
			},
			selectors: map[int]digest.Digest{
				0: dgst("sel0"),
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(2), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(2), *g0.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	// repeat, cache is matched

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-no-cache",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1-no-cache",
				})},
			},
			selectors: map[int]digest.Digest{
				0: dgst("sel1"),
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
		}),
	}
	g1.Vertex.(*vertex).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(2), *g1.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(0), *g1.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(2), cacheManager.loadCounter)

	require.NoError(t, j1.Discard())
	j1 = nil
}

func TestCacheExporting(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtxSum(1, vtxOpt{
			inputs: []Edge{
				{Vertex: vtxConst(2, vtxOpt{})},
				{Vertex: vtxConst(3, vtxOpt{})},
			},
		}),
	}

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 6)

	require.NoError(t, j0.Discard())
	j0 = nil

	expTarget := newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()

	require.Equal(t, len(expTarget.records), 3)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 0)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[0].links, 2)
	require.Equal(t, expTarget.records[1].links, 0)
	require.Equal(t, expTarget.records[2].links, 0)

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	res, err = j1.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 6)

	require.NoError(t, j1.Discard())
	j1 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()
	// the order of the records isn't really significant
	require.Equal(t, len(expTarget.records), 3)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 0)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[0].links, 2)
	require.Equal(t, expTarget.records[1].links, 0)
	require.Equal(t, expTarget.records[2].links, 0)
}

func TestCacheExportingModeMin(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtxSum(1, vtxOpt{
			inputs: []Edge{
				{Vertex: vtxSum(2, vtxOpt{
					inputs: []Edge{
						{Vertex: vtxConst(3, vtxOpt{})},
					},
				})},
				{Vertex: vtxConst(5, vtxOpt{})},
			},
		}),
	}

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 11)

	require.NoError(t, j0.Discard())
	j0 = nil

	expTarget := newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(false))
	require.NoError(t, err)

	expTarget.normalize()

	require.Equal(t, len(expTarget.records), 4)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 0)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[3].results, 0)
	require.Equal(t, expTarget.records[0].links, 2)
	require.Equal(t, expTarget.records[1].links, 1)
	require.Equal(t, expTarget.records[2].links, 0)
	require.Equal(t, expTarget.records[3].links, 0)

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	res, err = j1.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 11)

	require.NoError(t, j1.Discard())
	j1 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(false))
	require.NoError(t, err)

	expTarget.normalize()
	// the order of the records isn't really significant
	require.Equal(t, len(expTarget.records), 4)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 0)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[3].results, 0)
	require.Equal(t, expTarget.records[0].links, 2)
	require.Equal(t, expTarget.records[1].links, 1)
	require.Equal(t, expTarget.records[2].links, 0)
	require.Equal(t, expTarget.records[3].links, 0)

	// one more check with all mode
	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	res, err = j2.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 11)

	require.NoError(t, j2.Discard())
	j2 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()
	// the order of the records isn't really significant
	require.Equal(t, len(expTarget.records), 4)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 1)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[3].results, 0)
	require.Equal(t, expTarget.records[0].links, 2)
	require.Equal(t, expTarget.records[1].links, 1)
	require.Equal(t, expTarget.records[2].links, 0)
	require.Equal(t, expTarget.records[3].links, 0)
}

func TestSlowCacheAvoidAccess(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			cachePreFunc: func(context.Context) error {
				select {
				case <-time.After(50 * time.Millisecond):
				case <-ctx.Done():
				}
				return nil
			},
			value: "result0",
			inputs: []Edge{{
				Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
					inputs: []Edge{
						{Vertex: vtx(vtxOpt{
							name:         "v2",
							cacheKeySeed: "seed2",
							value:        "result2",
						})},
					},
					selectors: map[int]digest.Digest{
						0: dgst("sel0"),
					},
					slowCacheCompute: map[int]ResultBasedCacheFunc{
						0: digestFromResult,
					},
				}),
			}},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	g0.Vertex.(*vertex).setupCallCounters()

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	res, err = j1.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j1.Discard())
	j1 = nil

	require.Equal(t, int64(3), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(0), *g0.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(1), cacheManager.loadCounter)
}

// TestSlowCacheAvoidExecOnCache tests a regression where an input with
// possible matches and a content based checksum should not try to checksum
// before other inputs with no keys have at least made into a slow state.
// moby/buildkit#648
func TestSlowCacheAvoidLoadOnCache(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "vmain",
			cacheKeySeed: "seedmain",
			value:        "resultmain",
			inputs: []Edge{{
				Vertex: vtx(vtxOpt{
					name:         "v0",
					cacheKeySeed: "seed0",
					value:        "result0",
					inputs: []Edge{
						{
							Vertex: vtx(vtxOpt{
								name:         "v1",
								cacheKeySeed: "seed1",
								value:        "result1",
								inputs: []Edge{{
									Vertex: vtx(vtxOpt{
										name:         "v3",
										cacheKeySeed: "seed3",
										value:        "result3",
									}),
								}},
								slowCacheCompute: map[int]ResultBasedCacheFunc{
									0: digestFromResult,
								},
							}),
						},
						{
							Vertex: vtx(vtxOpt{
								name:         "v2",
								cacheKeySeed: "seed2",
								value:        "result2",
							}),
						},
					},
					slowCacheCompute: map[int]ResultBasedCacheFunc{
						1: digestFromResult,
					},
				}),
			}},
		}),
	}
	g0.Vertex.(*vertex).setupCallCounters()

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "resultmain")

	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	// the switch of the cache key for v3 forcing it to be reexecuted
	// testing that this does not cause v2 to be reloaded for cache for its
	// checksum recalculation
	g0 = Edge{
		Vertex: vtx(vtxOpt{
			name:         "vmain",
			cacheKeySeed: "seedmain",
			value:        "resultmain",
			inputs: []Edge{{
				Vertex: vtx(vtxOpt{
					name:         "v0",
					cacheKeySeed: "seed0",
					value:        "result0",
					inputs: []Edge{
						{
							Vertex: vtx(vtxOpt{
								name:         "v1",
								cacheKeySeed: "seed1",
								value:        "result1",
								inputs: []Edge{{
									Vertex: vtx(vtxOpt{
										name:         "v3",
										cacheKeySeed: "seed3-new",
										value:        "result3",
									}),
								}},
								execPreFunc: func(context.Context) error {
									select {
									case <-time.After(50 * time.Millisecond):
									case <-ctx.Done():
									}
									return nil
								},
								slowCacheCompute: map[int]ResultBasedCacheFunc{
									0: digestFromResult,
								},
							}),
						},
						{
							Vertex: vtx(vtxOpt{
								name:         "v2",
								cacheKeySeed: "seed2",
								value:        "result2",
							}),
						},
					},
					slowCacheCompute: map[int]ResultBasedCacheFunc{
						1: digestFromResult,
					},
				}),
			}},
		}),
	}

	g0.Vertex.(*vertex).setupCallCounters()

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	res, err = j1.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "resultmain")

	require.NoError(t, j1.Discard())
	j1 = nil

	require.Equal(t, int64(5), *g0.Vertex.(*vertex).cacheCallCount)
	require.Equal(t, int64(1), *g0.Vertex.(*vertex).execCallCount)
	require.Equal(t, int64(1), cacheManager.loadCounter)
}

func TestCacheMultipleMaps(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			cacheKeySeeds: []func() string{
				func() string { return "seed1" },
				func() string { return "seed2" },
			},
			value: "result0",
		}),
	}
	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j0.Discard())
	j0 = nil

	expTarget := newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()
	require.Equal(t, len(expTarget.records), 3)

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	called := false
	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v1",
			cacheKeySeed: "seed1",
			cacheKeySeeds: []func() string{
				func() string { called = true; return "seed3" },
			},
			value: "result0-not-cached",
		}),
	}

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j1.Discard())
	j1 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	require.Equal(t, len(expTarget.records), 3)
	require.Equal(t, called, false)

	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v2",
			cacheKeySeed: "seed3",
			cacheKeySeeds: []func() string{
				func() string { called = true; return "seed2" },
			},
			value: "result0-not-cached",
		}),
	}

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j2.Discard())
	j2 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	require.Equal(t, len(expTarget.records), 3)
	require.Equal(t, called, true)
}

func TestCacheInputMultipleMaps(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{{
				Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					cacheKeySeeds: []func() string{
						func() string { return "seed2" },
					},
					value: "result1",
				}),
			}},
		}),
	}
	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	expTarget := newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()
	require.Equal(t, len(expTarget.records), 3)

	require.NoError(t, j0.Discard())
	j0 = nil

	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0-no-cache",
			inputs: []Edge{{
				Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1.changed",
					cacheKeySeeds: []func() string{
						func() string { return "seed2" },
					},
					value: "result1-no-cache",
				}),
			}},
		}),
	}
	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()
	require.Equal(t, len(expTarget.records), 3)

	require.NoError(t, j1.Discard())
	j1 = nil
}

func TestCacheExportingPartialSelector(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1",
					value:        "result1",
				})},
			},
			selectors: map[int]digest.Digest{
				0: dgst("sel0"),
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
		}),
	}

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j0.Discard())
	j0 = nil

	expTarget := newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()
	require.Equal(t, len(expTarget.records), 3)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 0)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[0].links, 2)
	require.Equal(t, expTarget.records[1].links, 0)
	require.Equal(t, expTarget.records[2].links, 0)

	// repeat so that all coming from cache are retained
	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := g0

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j1.Discard())
	j1 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()

	// the order of the records isn't really significant
	require.Equal(t, len(expTarget.records), 3)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 0)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[0].links, 2)
	require.Equal(t, expTarget.records[1].links, 0)
	require.Equal(t, expTarget.records[2].links, 0)

	// repeat with forcing a slow key recomputation
	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{Vertex: vtx(vtxOpt{
					name:         "v1",
					cacheKeySeed: "seed1-net",
					value:        "result1",
				})},
			},
			selectors: map[int]digest.Digest{
				0: dgst("sel0"),
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				0: digestFromResult,
			},
		}),
	}

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j2.Discard())
	j2 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()

	// the order of the records isn't really significant
	// adds one
	require.Equal(t, len(expTarget.records), 4)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 0)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[3].results, 0)
	require.Equal(t, expTarget.records[0].links, 3)
	require.Equal(t, expTarget.records[1].links, 0)
	require.Equal(t, expTarget.records[2].links, 0)
	require.Equal(t, expTarget.records[3].links, 0)

	// repeat with a wrapper
	j3, err := l.NewJob("j3")
	require.NoError(t, err)

	defer func() {
		if j3 != nil {
			j3.Discard()
		}
	}()

	g3 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v2",
			cacheKeySeed: "seed2",
			value:        "result2",
			inputs:       []Edge{g2},
		},
		),
	}

	res, err = j3.Build(ctx, g3)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result2")

	require.NoError(t, j3.Discard())
	j3 = nil

	expTarget = newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()

	// adds one extra result
	// the order of the records isn't really significant
	require.Equal(t, len(expTarget.records), 5)
	require.Equal(t, expTarget.records[0].results, 1)
	require.Equal(t, expTarget.records[1].results, 1)
	require.Equal(t, expTarget.records[2].results, 0)
	require.Equal(t, expTarget.records[3].results, 0)
	require.Equal(t, expTarget.records[4].results, 0)
	require.Equal(t, expTarget.records[0].links, 1)
	require.Equal(t, expTarget.records[1].links, 3)
	require.Equal(t, expTarget.records[2].links, 0)
	require.Equal(t, expTarget.records[3].links, 0)
	require.Equal(t, expTarget.records[4].links, 0)
}

func TestCacheExportingMergedKey(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{
					Vertex: vtx(vtxOpt{
						name:         "v1",
						cacheKeySeed: "seed1",
						value:        "result1",
						inputs: []Edge{
							{
								Vertex: vtx(vtxOpt{
									name:         "v2",
									cacheKeySeed: "seed2",
									value:        "result2",
								}),
							},
						},
						slowCacheCompute: map[int]ResultBasedCacheFunc{
							0: digestFromResult,
						},
					}),
				},
				{
					Vertex: vtx(vtxOpt{
						name:         "v1-diff",
						cacheKeySeed: "seed1",
						value:        "result1",
						inputs: []Edge{
							{
								Vertex: vtx(vtxOpt{
									name:         "v3",
									cacheKeySeed: "seed3",
									value:        "result2",
								}),
							},
						},
						slowCacheCompute: map[int]ResultBasedCacheFunc{
							0: digestFromResult,
						},
					}),
				},
			},
		}),
	}

	res, err := j0.Build(ctx, g0)
	require.NoError(t, err)
	require.Equal(t, unwrap(res), "result0")

	require.NoError(t, j0.Discard())
	j0 = nil

	expTarget := newTestExporterTarget()

	_, err = res.CacheKeys()[0].Exporter.ExportTo(ctx, expTarget, testExporterOpts(true))
	require.NoError(t, err)

	expTarget.normalize()

	require.Equal(t, len(expTarget.records), 5)
}

// moby/buildkit#434
func TestMergedEdgesLookup(t *testing.T) {
	t.Parallel()

	// this test requires multiple runs to trigger the race
	for i := 0; i < 20; i++ {
		func() {
			ctx := context.TODO()

			cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

			l := NewSolver(SolverOpt{
				ResolveOpFunc: testOpResolver,
				DefaultCache:  cacheManager,
			})
			defer l.Close()

			j0, err := l.NewJob("j0")
			require.NoError(t, err)

			defer func() {
				if j0 != nil {
					j0.Discard()
				}
			}()

			g := Edge{
				Vertex: vtxSum(3, vtxOpt{inputs: []Edge{
					{Vertex: vtxSum(0, vtxOpt{inputs: []Edge{
						{Vertex: vtxSum(2, vtxOpt{inputs: []Edge{
							{Vertex: vtxConst(2, vtxOpt{})},
						}})},
						{Vertex: vtxConst(0, vtxOpt{})},
					}})},
					{Vertex: vtxSum(2, vtxOpt{inputs: []Edge{
						{Vertex: vtxConst(2, vtxOpt{})},
					}})},
				}}),
			}
			g.Vertex.(*vertexSum).setupCallCounters()

			res, err := j0.Build(ctx, g)
			require.NoError(t, err)
			require.Equal(t, unwrapInt(res), 11)

			require.Equal(t, int64(7), *g.Vertex.(*vertexSum).cacheCallCount)
			require.Equal(t, int64(0), cacheManager.loadCounter)

			require.NoError(t, j0.Discard())
			j0 = nil
		}()
	}
}

func TestMergedEdgesCycle(t *testing.T) {
	t.Parallel()

	for i := 0; i < 20; i++ {
		ctx := context.TODO()

		cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

		l := NewSolver(SolverOpt{
			ResolveOpFunc: testOpResolver,
			DefaultCache:  cacheManager,
		})
		defer l.Close()

		j0, err := l.NewJob("j0")
		require.NoError(t, err)

		defer func() {
			if j0 != nil {
				j0.Discard()
			}
		}()

		// 2 different vertices, va and vb, both with the same cache key
		va := vtxAdd(2, vtxOpt{name: "va", inputs: []Edge{
			{Vertex: vtxConst(3, vtxOpt{})},
			{Vertex: vtxConst(4, vtxOpt{})},
		}})
		vb := vtxAdd(2, vtxOpt{name: "vb", inputs: []Edge{
			{Vertex: vtxConst(3, vtxOpt{})},
			{Vertex: vtxConst(4, vtxOpt{})},
		}})

		// 4 edges va[0], va[1], vb[0], vb[1]
		// by ordering them like this, we try and trigger merge va[0]->vb[0] and
		// vb[1]->va[1] to cause a cycle
		g := Edge{
			Vertex: vtxSum(1, vtxOpt{inputs: []Edge{
				{Vertex: va, Index: 1}, // 6
				{Vertex: vb, Index: 0}, // 5
				{Vertex: va, Index: 0}, // 5
				{Vertex: vb, Index: 1}, // 6
			}}),
		}
		g.Vertex.(*vertexSum).setupCallCounters()

		res, err := j0.Build(ctx, g)
		require.NoError(t, err)
		require.Equal(t, 23, unwrapInt(res))

		require.NoError(t, j0.Discard())
		j0 = nil
	}
}

func TestMergedEdgesCycleMultipleOwners(t *testing.T) {
	t.Parallel()

	for i := 0; i < 20; i++ {
		ctx := context.TODO()

		cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

		l := NewSolver(SolverOpt{
			ResolveOpFunc: testOpResolver,
			DefaultCache:  cacheManager,
		})
		defer l.Close()

		j0, err := l.NewJob("j0")
		require.NoError(t, err)

		defer func() {
			if j0 != nil {
				j0.Discard()
			}
		}()

		va := vtxAdd(2, vtxOpt{name: "va", inputs: []Edge{
			{Vertex: vtxConst(3, vtxOpt{})},
			{Vertex: vtxConst(4, vtxOpt{})},
			{Vertex: vtxConst(5, vtxOpt{})},
		}})
		vb := vtxAdd(2, vtxOpt{name: "vb", inputs: []Edge{
			{Vertex: vtxConst(3, vtxOpt{})},
			{Vertex: vtxConst(4, vtxOpt{})},
			{Vertex: vtxConst(5, vtxOpt{})},
		}})
		vc := vtxAdd(2, vtxOpt{name: "vc", inputs: []Edge{
			{Vertex: vtxConst(3, vtxOpt{})},
			{Vertex: vtxConst(4, vtxOpt{})},
			{Vertex: vtxConst(5, vtxOpt{})},
		}})

		g := Edge{
			Vertex: vtxSum(1, vtxOpt{inputs: []Edge{
				// we trigger merge va[0]->vb[0] and va[1]->vc[1] so that va gets
				// been merged twice
				{Vertex: vb, Index: 0}, // 5
				{Vertex: va, Index: 0}, // 5

				{Vertex: vc, Index: 1}, // 6
				{Vertex: va, Index: 1}, // 6

				// then we trigger another merge via the first owner vb[1]->va[1]
				// that must be flipped
				{Vertex: va, Index: 2}, // 7
				{Vertex: vb, Index: 2}, // 7
			}}),
		}
		g.Vertex.(*vertexSum).setupCallCounters()

		res, err := j0.Build(ctx, g)
		require.NoError(t, err)
		require.Equal(t, 37, unwrapInt(res))

		require.NoError(t, j0.Discard())
		j0 = nil
	}
}

func TestCacheLoadError(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()

	cacheManager := newTrackingCacheManager(NewInMemoryCacheManager())

	l := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
		DefaultCache:  cacheManager,
	})
	defer l.Close()

	j0, err := l.NewJob("j0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g := Edge{
		Vertex: vtxSum(3, vtxOpt{inputs: []Edge{
			{Vertex: vtxSum(0, vtxOpt{inputs: []Edge{
				{Vertex: vtxSum(2, vtxOpt{inputs: []Edge{
					{Vertex: vtxConst(2, vtxOpt{})},
				}})},
				{Vertex: vtxConst(0, vtxOpt{})},
			}})},
			{Vertex: vtxSum(2, vtxOpt{inputs: []Edge{
				{Vertex: vtxConst(2, vtxOpt{})},
			}})},
		}}),
	}
	g.Vertex.(*vertexSum).setupCallCounters()

	res, err := j0.Build(ctx, g)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 11)
	require.Equal(t, int64(7), *g.Vertex.(*vertexSum).cacheCallCount)
	require.Equal(t, int64(5), *g.Vertex.(*vertexSum).execCallCount)
	require.Equal(t, int64(0), cacheManager.loadCounter)

	require.NoError(t, j0.Discard())
	j0 = nil

	// repeat with cache
	j1, err := l.NewJob("j1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := g

	g1.Vertex.(*vertexSum).setupCallCounters()

	res, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 11)
	require.Equal(t, int64(7), *g.Vertex.(*vertexSum).cacheCallCount)
	require.Equal(t, int64(0), *g.Vertex.(*vertexSum).execCallCount)
	require.Equal(t, int64(1), cacheManager.loadCounter)

	require.NoError(t, j1.Discard())
	j1 = nil

	// repeat with cache but loading will now fail
	j2, err := l.NewJob("j2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := g

	g2.Vertex.(*vertexSum).setupCallCounters()

	cacheManager.forceFail = true

	res, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.Equal(t, unwrapInt(res), 11)
	require.Equal(t, int64(7), *g.Vertex.(*vertexSum).cacheCallCount)
	require.Equal(t, int64(5), *g.Vertex.(*vertexSum).execCallCount)
	require.Equal(t, int64(6), cacheManager.loadCounter)

	require.NoError(t, j2.Discard())
	j2 = nil
}

func TestInputRequestDeadlock(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	j0, err := s.NewJob("job0")
	require.NoError(t, err)

	defer func() {
		if j0 != nil {
			j0.Discard()
		}
	}()

	g0 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0",
			value:        "result0",
			inputs: []Edge{
				{
					Vertex: vtx(vtxOpt{
						name:         "v1",
						cacheKeySeed: "seed1",
						value:        "result1",
					}),
				},
				{
					Vertex: vtx(vtxOpt{
						name:         "v2",
						cacheKeySeed: "seed2",
						value:        "result2",
					}),
				},
			},
		}),
	}

	_, err = j0.Build(ctx, g0)
	require.NoError(t, err)
	require.NoError(t, j0.Discard())
	j0 = nil

	j1, err := s.NewJob("job1")
	require.NoError(t, err)

	defer func() {
		if j1 != nil {
			j1.Discard()
		}
	}()

	g1 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0-1",
			value:        "result0",
			inputs: []Edge{
				{
					Vertex: vtx(vtxOpt{
						name:         "v1",
						cacheKeySeed: "seed1-1",
						value:        "result1",
					}),
				},
				{
					Vertex: vtx(vtxOpt{
						name:         "v2",
						cacheKeySeed: "seed2-1",
						value:        "result2",
					}),
				},
			},
		}),
	}

	_, err = j1.Build(ctx, g1)
	require.NoError(t, err)
	require.NoError(t, j1.Discard())
	j1 = nil

	j2, err := s.NewJob("job2")
	require.NoError(t, err)

	defer func() {
		if j2 != nil {
			j2.Discard()
		}
	}()

	g2 := Edge{
		Vertex: vtx(vtxOpt{
			name:         "v0",
			cacheKeySeed: "seed0-1",
			value:        "result0",
			inputs: []Edge{
				{
					Vertex: vtx(vtxOpt{
						name:         "v1",
						cacheKeySeed: "seed1",
						value:        "result1",
					}),
				},
				{
					Vertex: vtx(vtxOpt{
						name:         "v2",
						cacheKeySeed: "seed2-1",
						value:        "result2",
					}),
				},
			},
			slowCacheCompute: map[int]ResultBasedCacheFunc{
				1: digestFromResult,
			},
		}),
	}

	_, err = j2.Build(ctx, g2)
	require.NoError(t, err)
	require.NoError(t, j2.Discard())
	j2 = nil
}

func TestUnknownBuildID(t *testing.T) {
	s := NewSolver(SolverOpt{
		ResolveOpFunc: testOpResolver,
	})
	defer s.Close()

	_, err := s.Get(identity.NewID())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no such job")
}

func generateSubGraph(nodes int) (Edge, int) {
	if nodes == 1 {
		value := rand.Int() % 500 //nolint:gosec
		return Edge{Vertex: vtxConst(value, vtxOpt{})}, value
	}
	spread := rand.Int()%5 + 2 //nolint:gosec
	inc := int(math.Ceil(float64(nodes) / float64(spread)))
	if inc > nodes {
		inc = nodes
	}
	added := 1
	value := 0
	inputs := []Edge{}
	i := 0
	for {
		i++
		if added >= nodes {
			break
		}
		if added+inc > nodes {
			inc = nodes - added
		}
		e, v := generateSubGraph(inc)
		inputs = append(inputs, e)
		value += v
		added += inc
	}
	extra := rand.Int() % 500 //nolint:gosec
	value += extra
	return Edge{Vertex: vtxSum(extra, vtxOpt{inputs: inputs})}, value
}

type vtxOpt struct {
	name             string
	cacheKeySeed     string
	cacheKeySeeds    []func() string
	execDelay        time.Duration
	cacheDelay       time.Duration
	cachePreFunc     func(context.Context) error
	execPreFunc      func(context.Context) error
	inputs           []Edge
	value            string
	slowCacheCompute map[int]ResultBasedCacheFunc
	selectors        map[int]digest.Digest
	cacheSource      CacheManager
	ignoreCache      bool
}

func vtx(opt vtxOpt) *vertex {
	if opt.name == "" {
		opt.name = identity.NewID()
	}
	if opt.cacheKeySeed == "" {
		opt.cacheKeySeed = identity.NewID()
	}
	return &vertex{opt: opt}
}

type vertex struct {
	opt vtxOpt

	cacheCallCount *int64
	execCallCount  *int64
}

func (v *vertex) Digest() digest.Digest {
	return digest.FromBytes([]byte(v.opt.name))
}
func (v *vertex) Sys() interface{} {
	return v
}
func (v *vertex) Inputs() []Edge {
	return v.opt.inputs
}
func (v *vertex) Name() string {
	return v.opt.name
}
func (v *vertex) Options() VertexOptions {
	var cache []CacheManager
	if v.opt.cacheSource != nil {
		cache = append(cache, v.opt.cacheSource)
	}
	return VertexOptions{
		CacheSources: cache,
		IgnoreCache:  v.opt.ignoreCache,
	}
}

func (v *vertex) setupCallCounters() {
	var cacheCount int64
	var execCount int64

	v.setCallCounters(&cacheCount, &execCount)
}

func (v *vertex) setCallCounters(cacheCount, execCount *int64) {
	v.cacheCallCount = cacheCount
	v.execCallCount = execCount

	for _, inp := range v.opt.inputs {
		var v *vertex
		switch vv := inp.Vertex.(type) {
		case *vertex:
			v = vv
		case *vertexSum:
			v = vv.vertex
		case *vertexAdd:
			v = vv.vertex
		case *vertexConst:
			v = vv.vertex
		case *vertexSubBuild:
			v = vv.vertex
		}
		v.setCallCounters(cacheCount, execCount)
	}
}

func (v *vertex) cacheMap(ctx context.Context) error {
	if f := v.opt.cachePreFunc; f != nil {
		if err := f(ctx); err != nil {
			return err
		}
	}
	if v.cacheCallCount != nil {
		atomic.AddInt64(v.cacheCallCount, 1)
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
	}
	select {
	case <-time.After(v.opt.cacheDelay):
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	return nil
}

func (v *vertex) CacheMap(ctx context.Context, g session.Group, index int) (*CacheMap, bool, error) {
	if index == 0 {
		if err := v.cacheMap(ctx); err != nil {
			return nil, false, err
		}
		return v.makeCacheMap(), len(v.opt.cacheKeySeeds) == index, nil
	}
	return &CacheMap{
		Digest: digest.FromBytes([]byte(fmt.Sprintf("seed:%s", v.opt.cacheKeySeeds[index-1]()))),
	}, len(v.opt.cacheKeySeeds) == index, nil
}

func (v *vertex) exec(ctx context.Context, inputs []Result) error {
	if len(inputs) != len(v.Inputs()) {
		return errors.Errorf("invalid number of inputs")
	}
	if f := v.opt.execPreFunc; f != nil {
		if err := f(ctx); err != nil {
			return err
		}
	}
	if v.execCallCount != nil {
		atomic.AddInt64(v.execCallCount, 1)
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
	}
	select {
	case <-time.After(v.opt.execDelay):
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	return nil
}

func (v *vertex) Exec(ctx context.Context, g session.Group, inputs []Result) (outputs []Result, err error) {
	if err := v.exec(ctx, inputs); err != nil {
		return nil, err
	}
	return []Result{&dummyResult{id: identity.NewID(), value: v.opt.value}}, nil
}

func (v *vertex) Acquire(ctx context.Context) (ReleaseFunc, error) {
	return func() {}, nil
}

func (v *vertex) makeCacheMap() *CacheMap {
	m := &CacheMap{
		Digest: digest.FromBytes([]byte(fmt.Sprintf("seed:%s", v.opt.cacheKeySeed))),
		Deps: make([]struct {
			Selector          digest.Digest
			ComputeDigestFunc ResultBasedCacheFunc
			PreprocessFunc    PreprocessFunc
		}, len(v.Inputs())),
	}
	for i, f := range v.opt.slowCacheCompute {
		m.Deps[i].ComputeDigestFunc = f
	}
	for i, dgst := range v.opt.selectors {
		m.Deps[i].Selector = dgst
	}
	return m
}

// vtxConst returns a vertex that outputs a constant integer
func vtxConst(v int, opt vtxOpt) *vertexConst {
	if opt.cacheKeySeed == "" {
		opt.cacheKeySeed = fmt.Sprintf("const-%d", v)
	}
	if opt.name == "" {
		opt.name = opt.cacheKeySeed + "-" + identity.NewID()
	}
	return &vertexConst{vertex: vtx(opt), value: v}
}

type vertexConst struct {
	*vertex
	value int
}

func (v *vertexConst) Sys() interface{} {
	return v
}

func (v *vertexConst) Exec(ctx context.Context, g session.Group, inputs []Result) (outputs []Result, err error) {
	if err := v.exec(ctx, inputs); err != nil {
		return nil, err
	}
	return []Result{&dummyResult{id: identity.NewID(), intValue: v.value}}, nil
}

func (v *vertexConst) Acquire(ctx context.Context) (ReleaseFunc, error) {
	return func() {}, nil
}

// vtxSum returns a vertex that outputs sum of its inputs plus a constant
func vtxSum(v int, opt vtxOpt) *vertexSum {
	if opt.cacheKeySeed == "" {
		opt.cacheKeySeed = fmt.Sprintf("sum-%d-%d", v, len(opt.inputs))
	}
	if opt.name == "" {
		opt.name = opt.cacheKeySeed + "-" + identity.NewID()
	}
	return &vertexSum{vertex: vtx(opt), value: v}
}

type vertexSum struct {
	*vertex
	value int
}

func (v *vertexSum) Sys() interface{} {
	return v
}

func (v *vertexSum) Exec(ctx context.Context, g session.Group, inputs []Result) (outputs []Result, err error) {
	if err := v.exec(ctx, inputs); err != nil {
		return nil, err
	}
	s := v.value
	for _, inp := range inputs {
		r, ok := inp.Sys().(*dummyResult)
		if !ok {
			return nil, errors.Errorf("invalid input type: %T", inp.Sys())
		}
		s += r.intValue
	}
	return []Result{&dummyResult{id: identity.NewID(), intValue: s}}, nil
}

func (v *vertexSum) Acquire(ctx context.Context) (ReleaseFunc, error) {
	return func() {}, nil
}

// vtxAdd returns a vertex that outputs each input plus a constant
func vtxAdd(v int, opt vtxOpt) *vertexAdd {
	if opt.cacheKeySeed == "" {
		opt.cacheKeySeed = fmt.Sprintf("add-%d-%d", v, len(opt.inputs))
	}
	if opt.name == "" {
		opt.name = opt.cacheKeySeed + "-" + identity.NewID()
	}
	return &vertexAdd{vertex: vtx(opt), value: v}
}

type vertexAdd struct {
	*vertex
	value int
}

func (v *vertexAdd) Sys() interface{} {
	return v
}

func (v *vertexAdd) Exec(ctx context.Context, g session.Group, inputs []Result) (outputs []Result, err error) {
	if err := v.exec(ctx, inputs); err != nil {
		return nil, err
	}
	for _, inp := range inputs {
		r, ok := inp.Sys().(*dummyResult)
		if !ok {
			return nil, errors.Errorf("invalid input type: %T", inp.Sys())
		}
		outputs = append(outputs, &dummyResult{id: identity.NewID(), intValue: r.intValue + v.value})
	}
	return outputs, nil
}

func (v *vertexAdd) Acquire(ctx context.Context) (ReleaseFunc, error) {
	return func() {}, nil
}

func vtxSubBuild(g Edge, opt vtxOpt) *vertexSubBuild {
	if opt.cacheKeySeed == "" {
		opt.cacheKeySeed = fmt.Sprintf("sub-%s", identity.NewID())
	}
	if opt.name == "" {
		opt.name = opt.cacheKeySeed + "-" + identity.NewID()
	}
	return &vertexSubBuild{vertex: vtx(opt), g: g}
}

type vertexSubBuild struct {
	*vertex
	g Edge
	b Builder
}

func (v *vertexSubBuild) Sys() interface{} {
	return v
}

func (v *vertexSubBuild) Exec(ctx context.Context, g session.Group, inputs []Result) (outputs []Result, err error) {
	if err := v.exec(ctx, inputs); err != nil {
		return nil, err
	}
	res, err := v.b.Build(ctx, v.g)
	if err != nil {
		return nil, err
	}
	return []Result{res}, nil
}

func (v *vertexSubBuild) Acquire(ctx context.Context) (ReleaseFunc, error) {
	return func() {}, nil
}

//nolint:unused
func printGraph(e Edge, pfx string) {
	name := e.Vertex.Name()
	fmt.Printf("%s %d %s\n", pfx, e.Index, name)
	for _, inp := range e.Vertex.Inputs() {
		printGraph(inp, pfx+"-->")
	}
}

type dummyResult struct {
	id       string
	value    string
	intValue int
}

func (r *dummyResult) ID() string                    { return r.id }
func (r *dummyResult) Release(context.Context) error { return nil }
func (r *dummyResult) Sys() interface{}              { return r }
func (r *dummyResult) Clone() Result                 { return r }

func testOpResolver(v Vertex, b Builder) (Op, error) {
	if op, ok := v.Sys().(Op); ok {
		if vtx, ok := op.(*vertexSubBuild); ok {
			vtx.b = b
		}
		return op, nil
	}

	return nil, errors.Errorf("invalid vertex")
}

func unwrap(res Result) string {
	r, ok := res.Sys().(*dummyResult)
	if !ok {
		return "unwrap-error"
	}
	return r.value
}

func unwrapInt(res Result) int {
	r, ok := res.Sys().(*dummyResult)
	if !ok {
		return -1e6
	}
	return r.intValue
}

func blockingFuncion(i int) func(context.Context) error {
	limit := int64(i)
	block := make(chan struct{})
	return func(context.Context) error {
		if atomic.AddInt64(&limit, -1) == 0 {
			close(block)
		}
		<-block
		return nil
	}
}

func newTrackingCacheManager(cm CacheManager) *trackingCacheManager {
	return &trackingCacheManager{CacheManager: cm}
}

type trackingCacheManager struct {
	CacheManager
	loadCounter int64
	forceFail   bool
}

func (cm *trackingCacheManager) Load(ctx context.Context, rec *CacheRecord) (Result, error) {
	atomic.AddInt64(&cm.loadCounter, 1)
	if cm.forceFail {
		return nil, errors.Errorf("force fail")
	}
	return cm.CacheManager.Load(ctx, rec)
}

func digestFromResult(ctx context.Context, res Result, _ session.Group) (digest.Digest, error) {
	return digest.FromBytes([]byte(unwrap(res))), nil
}

func testExporterOpts(all bool) CacheExportOpt {
	mode := CacheExportModeMin
	if all {
		mode = CacheExportModeMax
	}
	return CacheExportOpt{
		ResolveRemotes: func(ctx context.Context, res Result) ([]*Remote, error) {
			if dr, ok := res.Sys().(*dummyResult); ok {
				return []*Remote{{Descriptors: []ocispecs.Descriptor{{
					Annotations: map[string]string{"value": fmt.Sprintf("%d", dr.intValue)},
				}}}}, nil
			}
			return nil, nil
		},
		Mode: mode,
	}
}

func newTestExporterTarget() *testExporterTarget {
	return &testExporterTarget{
		visited: map[interface{}]struct{}{},
	}
}

type testExporterTarget struct {
	visited map[interface{}]struct{}
	records []*testExporterRecord
}

func (t *testExporterTarget) Add(dgst digest.Digest) CacheExporterRecord {
	r := &testExporterRecord{dgst: dgst}
	t.records = append(t.records, r)
	return r
}

func (t *testExporterTarget) Visit(v interface{}) {
	t.visited[v] = struct{}{}
}

func (t *testExporterTarget) Visited(v interface{}) bool {
	_, ok := t.visited[v]
	return ok
}

func (t *testExporterTarget) normalize() {
	m := map[digest.Digest]struct{}{}
	rec := make([]*testExporterRecord, 0, len(t.records))
	for _, r := range t.records {
		if _, ok := m[r.dgst]; ok {
			for _, r2 := range t.records {
				delete(r2.linkMap, r.dgst)
				r2.links = len(r2.linkMap)
			}
			continue
		}
		m[r.dgst] = struct{}{}
		rec = append(rec, r)
	}
	t.records = rec
}

type testExporterRecord struct {
	dgst    digest.Digest
	results int
	links   int
	linkMap map[digest.Digest]struct{}
}

func (r *testExporterRecord) AddResult(_ digest.Digest, _ int, createdAt time.Time, result *Remote) {
	r.results++
}

func (r *testExporterRecord) LinkFrom(src CacheExporterRecord, index int, selector string) {
	if s, ok := src.(*testExporterRecord); ok {
		if r.linkMap == nil {
			r.linkMap = map[digest.Digest]struct{}{}
		}
		if _, ok := r.linkMap[s.dgst]; !ok {
			r.linkMap[s.dgst] = struct{}{}
			r.links++
		}
	}
}
