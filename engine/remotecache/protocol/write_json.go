//go:build go1.27 && goexperiment.jsonv2

package protocol

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"io"
)

// WriteJSON writes the JSON json.Marshal gives for v to w, as it is
// encoded: the encoder hands w its buffer each time the buffer fills, so the
// JSON is never in memory whole, and a value that encodes in place
// (dagql.TransferredValue.MarshalJSONTo) is never a buffer of its own. An
// error from w stops the encoding, and is returned.
func WriteJSON(w io.Writer, v any) error {
	return jsonv2.MarshalWrite(w, v, json.DefaultOptionsV1())
}
