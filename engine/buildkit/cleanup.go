package buildkit

import (
	"errors"

	"github.com/dagger/dagger/engine/slog"
)

type cleanups struct {
	funcs []cleanupFunc
}

type cleanupFunc struct {
	fn  func() error
	msg string
}

func (c *cleanups) add(msg string, f func() error) {
	c.funcs = append(c.funcs, cleanupFunc{fn: f, msg: msg})
}

func (c *cleanups) addNoErr(msg string, f func()) {
	c.add(msg, func() error {
		f()
		return nil
	})
}

func (c *cleanups) run() error {
	var rerr error
	for i := len(c.funcs) - 1; i >= 0; i-- {
		slog.ExtraDebug("running cleanup", "msg", c.funcs[i].msg)
		if err := c.funcs[i].fn(); err != nil {
			slog.ExtraDebug("cleanup failed", "msg", c.funcs[i].msg, "err", err)
			rerr = errors.Join(rerr, err)
		} else {
			slog.ExtraDebug("cleanup succeeded", "msg", c.funcs[i].msg)
		}
	}
	return rerr
}
