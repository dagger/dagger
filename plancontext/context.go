package plancontext

import (
	"crypto/sha256"
	"fmt"
)

// Context holds the execution context for a plan.
type Context struct {
	Platform  *platformContext
	FS        *fsContext
	LocalDirs *localDirContext
	TempDirs  *tempDirContext
	Secrets   *secretContext
	Services  *serviceContext
}

func New() *Context {
	return &Context{
		Platform: &platformContext{},
		FS: &fsContext{
			store: make(map[string]*FS),
		},
		LocalDirs: &localDirContext{
			store: []string{},
		},
		TempDirs: &tempDirContext{
			store: make(map[string]string),
		},
		Secrets: &secretContext{
			store: make(map[string]*Secret),
		},
		Services: &serviceContext{
			store: make(map[string]*Service),
		},
	}
}

func hashID(values ...string) string {
	hash := sha256.New()
	for _, v := range values {
		if _, err := hash.Write([]byte(v)); err != nil {
			panic(err)
		}
	}
	return fmt.Sprintf("%x", hash)
}
