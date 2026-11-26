//go:build go1.27 && goexperiment.jsonv2

package dagql

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
)

// decodeStoredRowHeader decodes a stored row's header, raw, into h as
// json.Unmarshal would, and refuses a field the header has no place for.
// It decodes raw in place: read through a reader, each header was first
// copied into a buffer of the decoder's own, which a restore did for every
// stored row.
func decodeStoredRowHeader(raw []byte, h *storedRowHeader) error {
	return jsonv2.Unmarshal(raw, h, json.DefaultOptionsV1(), jsonv2.RejectUnknownMembers(true))
}
