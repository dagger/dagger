package plancontext

import "sync"

type secretContext struct {
	l     sync.RWMutex
	store map[ContextKey]*Secret
}

type Secret struct {
	PlainText string
}

func (c *secretContext) Register(secret *Secret) ContextKey {
	c.l.Lock()
	defer c.l.Unlock()

	id := hashID(secret.PlainText)
	c.store[id] = secret
	return id
}

func (c *secretContext) Get(id ContextKey) *Secret {
	c.l.RLock()
	defer c.l.RUnlock()

	return c.store[id]
}

func (c *secretContext) List() []*Secret {
	c.l.RLock()
	defer c.l.RUnlock()

	secrets := make([]*Secret, 0, len(c.store))
	for _, s := range c.store {
		secrets = append(secrets, s)
	}

	return secrets
}
