package progress

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
)

func TestProgress(t *testing.T) {
	t.Parallel()
	s, err := calc(context.TODO(), 4, "calc")
	assert.NoError(t, err)
	assert.Equal(t, 10, s)

	eg, ctx := errgroup.WithContext(context.Background())

	pr, ctx, cancelProgress := NewContext(ctx)
	var trace trace
	eg.Go(func() error {
		return saveProgress(ctx, pr, &trace)
	})

	pw, _, ctx2 := NewFromContext(ctx, WithMetadata("tag", "foo"))
	s, err = calc(ctx2, 5, "calc")
	pw.Close()
	assert.NoError(t, err)
	assert.Equal(t, 15, s)

	cancelProgress(errors.WithStack(context.Canceled))
	err = eg.Wait()
	assert.NoError(t, err)

	assert.True(t, len(trace.items) > 5)
	assert.True(t, len(trace.items) <= 7)
	for _, p := range trace.items {
		v, ok := p.Meta("tag")
		assert.True(t, ok)
		assert.Equal(t, v.(string), "foo")
	}
}

func TestProgressNested(t *testing.T) {
	t.Parallel()
	eg, ctx := errgroup.WithContext(context.Background())
	pr, ctx, cancelProgress := NewContext(ctx)
	var trace trace
	eg.Go(func() error {
		return saveProgress(ctx, pr, &trace)
	})
	s, err := reduceCalc(ctx, 3)
	assert.NoError(t, err)
	assert.Equal(t, 6, s)

	cancelProgress(errors.WithStack(context.Canceled))

	err = eg.Wait()
	assert.NoError(t, err)

	assert.True(t, len(trace.items) > 9) // usually 14
	assert.True(t, len(trace.items) <= 15)
}

func calc(ctx context.Context, total int, name string) (int, error) {
	pw, _, ctx := NewFromContext(ctx)
	defer pw.Close()

	sum := 0
	pw.Write(name, Status{Action: "starting", Total: total})
	for i := 1; i <= total; i++ {
		select {
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		case <-time.After(10 * time.Millisecond):
		}
		if i == total {
			pw.Write(name, Status{Action: "done", Total: total, Current: total})
		} else {
			pw.Write(name, Status{Action: "calculating", Total: total, Current: i})
		}
		sum += i
	}

	return sum, nil
}

func reduceCalc(ctx context.Context, total int) (int, error) {
	eg, ctx := errgroup.WithContext(ctx)

	pw, _, ctx := NewFromContext(ctx)
	defer pw.Close()

	pw.Write("reduce", Status{Action: "starting"})

	// sync step
	sum, err := calc(ctx, total, "synccalc")
	if err != nil {
		return 0, err
	}
	// parallel steps
	for i := 0; i < 2; i++ {
		func(i int) {
			eg.Go(func() error {
				_, err := calc(ctx, total, fmt.Sprintf("calc-%d", i))
				return err
			})
		}(i)
	}
	if err := eg.Wait(); err != nil {
		return 0, err
	}
	return sum, nil
}

type trace struct {
	items []*Progress
}

func saveProgress(ctx context.Context, pr Reader, t *trace) error {
	for {
		p, err := pr.Read(ctx)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		t.items = append(t.items, p...)
	}
}
