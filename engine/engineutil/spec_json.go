//go:build go1.27 && goexperiment.jsonv2

package engineutil

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"io"
	"sync"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// specBufPool holds the buffers writeSpec encodes into. encoding/json's
// Encoder encodes into a buffer from a pool every json.Marshal in the engine
// shares, which drops a large buffer after a few small encodes, so each spec
// grew a buffer from scratch: about three times its size, for every exec.
var specBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// writeSpec writes spec as json.NewEncoder(w).Encode(spec) does.
func writeSpec(w io.Writer, spec *specs.Spec) error {
	buf := specBufPool.Get().(*bytes.Buffer)
	defer specBufPool.Put(buf)
	buf.Reset()
	if err := jsonv2.MarshalWrite(buf, spec, json.DefaultOptionsV1()); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}
