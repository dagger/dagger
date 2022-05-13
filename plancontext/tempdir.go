package plancontext

import (
	"os"
	"sync"
)

type tempDirContext struct {
	l     sync.RWMutex
	store map[string]string
}

func (c *tempDirContext) Add(dir, key string) {
	c.l.Lock()
	defer c.l.Unlock()

	c.store[key] = dir
}

func (c *tempDirContext) Get(key string) string {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[key]
}

func (c *tempDirContext) GetOrCreate(key string) (string, error) {
	c.l.Lock()
	defer c.l.Unlock()

	dir, ok := c.store[key]
	if !ok {
		tmpdir, err := os.MkdirTemp("", key)
		if err != nil {
			return "", err
		}
		dir = tmpdir
		c.store[key] = dir
	}

	return dir, nil
}

func (c *tempDirContext) Clean() {
	c.l.RLock()
	defer c.l.RUnlock()

	for _, s := range c.store {
		defer os.RemoveAll(s)
	}
}
