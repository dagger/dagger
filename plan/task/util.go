package task

import (
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
)

func vertexNamef(v *compiler.Value, format string, a ...interface{}) string {
	prefix := fmt.Sprintf("@%s@", v.Path().String())
	name := fmt.Sprintf(format, a...)
	return prefix + " " + name
}

func withCustomName(v *compiler.Value, format string, a ...interface{}) llb.ConstraintsOpt {
	return llb.WithCustomName(vertexNamef(v, format, a...))
}
