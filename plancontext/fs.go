package plancontext

import (
	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/pkg"
)

var fsIDPath = cue.MakePath(
	cue.Str("$dagger"),
	cue.Str("fs"),
	cue.Hid("_id", pkg.DaggerPackage),
)

func IsFSValue(v *compiler.Value) bool {
	return v.LookupPath(fsIDPath).Exists()
}

func IsFSScratchValue(v *compiler.Value) bool {
	return IsFSValue(v) && v.LookupPath(fsIDPath).Kind() == cue.NullKind
}

type FS struct {
	id string
}
