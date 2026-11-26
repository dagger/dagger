//go:build go1.27 && goexperiment.jsonv2

package dagql

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// countJSON returns the length of the JSON json.Marshal gives v, counted as
// the encoder hands its buffer on: the JSON is never in memory whole.
func countJSON(v any) (int64, error) {
	var n sizeCounter
	err := jsonv2.MarshalWrite(&n, v, json.DefaultOptionsV1())
	return int64(n), err
}

// MarshalJSONTo writes the value into the encoder of what holds it, the
// same JSON MarshalJSON gives: a bundle's encoder then writes each value in
// place, where MarshalJSON's bytes were copied whole into it, checked and
// copied again by every encoder above.
//
// It hands the encoder a pointer, which it encodes as the value: a value
// it would copy first, to address its fields.
func (v TransferredValue) MarshalJSONTo(enc *jsontext.Encoder) error {
	type plain TransferredValue
	if v.storedEnvelope == nil {
		return jsonv2.MarshalEncode(enc, (*plain)(&v))
	}
	stored, err := v.storedJSON()
	if err != nil {
		return err
	}
	return jsonv2.MarshalEncode(enc, &stored)
}

// MarshalJSONTo writes the body's JSON into the encoder of the value it is
// part of, a compressed body decompressed into the encoder's spare buffer
// (jsontext.Encoder.AvailableBuffer), which WriteValue copies from: the
// JSON is never a buffer of its own.
func (b storedJSONBody) MarshalJSONTo(enc *jsontext.Encoder) error {
	if b.compressed == nil {
		return enc.WriteValue(b.raw)
	}
	raw, err := b.decompress(enc.AvailableBuffer())
	if err != nil {
		return err
	}
	return enc.WriteValue(raw)
}
