package plancontext

import (
	"path/filepath"
	"sync"
)

type LocalDir struct {
	Path string
}

type localDirContext struct {
	l     sync.RWMutex
	store map[ContextKey]*LocalDir
}

func (c *localDirContext) Register(directory *LocalDir) ContextKey {
	c.l.Lock()
	defer c.l.Unlock()

	id := hashID(directory)
	c.store[id] = directory
	return id
}

func (c *localDirContext) Get(id ContextKey) *LocalDir {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[id]
}

func (c *localDirContext) List() []*LocalDir {
	c.l.RLock()
	defer c.l.RUnlock()

	directories := make([]*LocalDir, 0, len(c.store))
	for _, d := range c.store {
		directories = append(directories, d)
	}

	return directories
}

func (c *localDirContext) Paths() (map[string]string, error) {
	c.l.RLock()
	defer c.l.RUnlock()

	directories := make(map[string]string)
	for _, d := range c.store {
		abs, err := filepath.Abs(d.Path)
		if err != nil {
			return nil, err
		}

		directories[d.Path] = abs
	}

	return directories, nil
}
