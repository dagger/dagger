package plancontext

import (
	"crypto/sha256"
	"fmt"
)

// Context holds the execution context for a plan.
type Context struct {
	Platform *platformContext
	TempDirs *tempDirContext
	// Sockets  *socketContext
}

func New() *Context {
	return &Context{
		Platform: &platformContext{},
		TempDirs: &tempDirContext{
			store: make(map[string]string),
		},
		// Sockets: &socketContext{
		// 	store: make(map[string]*Socket),
		// },
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
