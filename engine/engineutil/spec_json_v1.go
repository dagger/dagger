//go:build !go1.27 || !goexperiment.jsonv2

package engineutil

import (
	"encoding/json"
	"io"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// writeSpec writes spec as json.NewEncoder(w).Encode(spec) does. Before Go
// 1.27, or without json v2, encoding/json's own pool keeps its buffers.
func writeSpec(w io.Writer, spec *specs.Spec) error {
	return json.NewEncoder(w).Encode(spec)
}
