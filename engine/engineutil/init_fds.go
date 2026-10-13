package engineutil

import (
	"fmt"
	"os"
	"slices"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/dagger/dagger/engine/distconsts"
)

// initFDEnvNames are the variables naming fds the engine passes the injected
// /.init (see initFDs).
var initFDEnvNames = []string{distconsts.InitTimingFDEnv, distconsts.SessionHelperStatusFDEnv}

// initFDs collects the pipe ends the engine passes the injected /.init. runc
// --preserve-fds passes them on from fd 3 in order, and an env var names each
// one's fd.
type initFDs struct {
	files []*os.File
	env   []string
}

// add passes w to /.init, named by envName; a nil w is skipped.
func (f *initFDs) add(envName string, w *os.File) {
	if w == nil {
		return
	}
	f.env = append(f.env, fmt.Sprintf("%s=%d", envName, 3+len(f.files)))
	f.files = append(f.files, w)
}

// withEnv returns a copy of spec whose process env names the fds. state.spec
// is left as is, so nothing else run from it sees them.
func (f *initFDs) withEnv(spec *specs.Spec) *specs.Spec {
	if len(f.env) == 0 {
		return spec
	}
	cp := *spec
	proc := *spec.Process
	proc.Env = append(slices.Clip(proc.Env), f.env...)
	cp.Process = &proc
	return &cp
}
