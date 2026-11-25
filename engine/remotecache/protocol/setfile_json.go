//go:build go1.27 && goexperiment.jsonv2

package protocol

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
)

// marshalInto writes the JSON json.Marshal gives for v into buf, in place:
// the encoder appends to buf's own unused bytes, and buf grows as it fills.
func marshalInto(buf *bytes.Buffer, v any) error {
	return jsonv2.MarshalWrite(buf, v, json.DefaultOptionsV1())
}
