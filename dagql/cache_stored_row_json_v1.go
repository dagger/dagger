//go:build !go1.27 || !goexperiment.jsonv2

package dagql

import (
	"bytes"
	"encoding/json"
)

// decodeStoredRowHeader decodes a stored row's header, raw, into h as
// json.Unmarshal would, and refuses a field the header has no place for.
// Before Go 1.27, or without json v2, it reads raw through a decoder.
func decodeStoredRowHeader(raw []byte, h *storedRowHeader) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(h)
}
