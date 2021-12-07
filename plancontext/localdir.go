package plancontext

import (
	"path/filepath"
	"sync"
)

type localDirContext struct {
	l     sync.RWMutex
	store []string
}

func (c *localDirContext) Add(dir string) {
	c.l.Lock()
	defer c.l.Unlock()

	c.store = append(c.store, dir)
}

func (c *localDirContext) Paths() (map[string]string, error) {
	c.l.RLock()
	defer c.l.RUnlock()

	directories := make(map[string]string)
	for _, d := range c.store {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, err
		}

		directories[d] = abs
	}

	return directories, nil
}
