package plancontext

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

type ContextKey string

// Context holds the execution context for a plan.
//
// Usage:
// ctx := plancontext.New()
// id := ctx.Secrets.Register("mysecret")
// secret := ctx.Secrets.Get(id)
type Context struct {
	Platform  *platformContext
	FS        *fsContext
	LocalDirs *localDirContext
	Secrets   *secretContext
	Services  *serviceContext
}

func New() *Context {
	return &Context{
		Platform: &platformContext{
			platform: defaultPlatform,
		},
		FS: &fsContext{
			store: make(map[ContextKey]*FS),
		},
		LocalDirs: &localDirContext{
			store: make(map[ContextKey]*LocalDir),
		},
		Secrets: &secretContext{
			store: make(map[ContextKey]*Secret),
		},
		Services: &serviceContext{
			store: make(map[ContextKey]*Service),
		},
	}
}

func hashID(v interface{}) ContextKey {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(data)
	return ContextKey(fmt.Sprintf("%x", hash))
}
