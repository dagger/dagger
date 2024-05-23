package buildkit

import (
	"errors"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/slog"
)

type Cleanups struct {
	funcs []func() error
}

type CleanupFunc struct {
	fn func() error
}

func (c *Cleanups) Add(msg string, f func() error) CleanupFunc {
	fOnce := sync.OnceValue(func() error {
		slog.ExtraDebug("running cleanup", "msg", msg)
		start := time.Now()
		err := f()
		if err != nil {
			slog.Error("cleanup failed", "msg", msg, "err", err, "duration", time.Since(start))
		} else {
			slog.ExtraDebug("cleanup succeeded", "msg", msg, "duration", time.Since(start))
		}
		return err
	})
	c.funcs = append(c.funcs, fOnce)
	return CleanupFunc{fOnce}
}

// ReAdd allows you to decide to run an already added cleanup function at a later time. Once readded,
// it will only be run at this time rather than both times.
// This is occasionally needed when you want to ensure some state is cleaned up right after it's created,
// but if more state is created later you ned to run this cleanup at that later time (e.g. closing a network
// connection in all cases).
func (c *Cleanups) ReAdd(f CleanupFunc) CleanupFunc {
	c.funcs = append(c.funcs, f.fn)
	return f
}

func (c *Cleanups) Run() error {
	var rerr error
	for i := len(c.funcs) - 1; i >= 0; i-- {
		rerr = errors.Join(rerr, c.funcs[i]())
	}
	return rerr
}

func IgnoreErrs(fn func() error, ignored ...error) func() error {
	return func() error {
		err := fn()
		for _, ig := range ignored {
			if errors.Is(err, ig) {
				return nil
			}
		}
		return err
	}
}

func Infallible(fn func()) func() error {
	return func() error {
		fn()
		return nil
	}
}
