//go:build !go1.27 || !goexperiment.jsonv2

package protocol

import (
	"bytes"
	"encoding/json"
)

// marshalInto writes the JSON json.Marshal gives for v into buf. Before Go
// 1.27, or without json v2, it is encoded apart and copied in.
func marshalInto(buf *bytes.Buffer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = buf.Write(data)
	return err
}
