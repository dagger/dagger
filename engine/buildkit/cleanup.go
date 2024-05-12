package buildkit

import "errors"

type cleanups struct {
	funcs []func() error
}

func (c *cleanups) add(f func() error) {
	c.funcs = append(c.funcs, f)
}

func (c *cleanups) addNoErr(f func()) {
	c.add(func() error {
		f()
		return nil
	})
}

func (c *cleanups) run() error {
	var rerr error
	for i := len(c.funcs) - 1; i >= 0; i-- {
		if err := c.funcs[i](); err != nil {
			rerr = errors.Join(rerr, err)
		}
	}
	return rerr
}
