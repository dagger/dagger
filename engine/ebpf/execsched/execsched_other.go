//go:build !(linux && (amd64 || arm64))

package execsched

import (
	"errors"

	"github.com/dagger/dagger/engine/ebpf/wcbpf"
)

// Program is exec_sched, which needs Linux on amd64 or arm64.
var Program = wcbpf.Program{Name: Name, Load: func() (wcbpf.Loaded, error) {
	return nil, errors.New("exec_sched needs Linux on amd64 or arm64")
}}
