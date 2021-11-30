package task

import (
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
)

func withCustomName(v *compiler.Value, format string, a ...interface{}) llb.ConstraintsOpt {
	prefix := fmt.Sprintf("@%s@", v.Path().String())
	name := fmt.Sprintf(format, a...)
	return llb.WithCustomName(prefix + " " + name)
}
