//go:build go1.27 && goexperiment.jsonv2

package dagql

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// MarshalJSONTo writes the value into the encoder of what holds it, the
// same JSON MarshalJSON gives: a bundle's encoder then writes each value in
// place, where MarshalJSON's bytes were copied whole into it, checked and
// copied again by every encoder above.
func (v TransferredValue) MarshalJSONTo(enc *jsontext.Encoder) error {
	type plain TransferredValue
	if v.storedEnvelope == nil {
		return jsonv2.MarshalEncode(enc, plain(v))
	}
	stored, err := v.storedJSON()
	if err != nil {
		return err
	}
	return jsonv2.MarshalEncode(enc, stored)
}
