package cc

import (
	"cuelang.org/go/cue"

	cueerrors "cuelang.org/go/cue/errors"
	"github.com/pkg/errors"
)

var (
	// Shared global compiler
	cc = &Compiler{}
)

func Compile(name string, src interface{}) (*Value, error) {
	return cc.Compile(name, src)
}

func EmptyStruct() (*Value, error) {
	return cc.EmptyStruct()
}

// FIXME can be refactored away now?
func Wrap(v cue.Value, inst *cue.Instance) *Value {
	return cc.Wrap(v, inst)
}

func Cue() *cue.Runtime {
	return cc.Cue()
}

func Err(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(cueerrors.Details(err, &cueerrors.Config{}))
}

func Lock() {
	cc.Lock()
}

func Unlock() {
	cc.Unlock()
}

func RLock() {
	cc.RLock()
}

func RUnlock() {
	cc.RUnlock()
}
