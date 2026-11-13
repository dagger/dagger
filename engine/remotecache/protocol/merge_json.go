//go:build go1.27 && goexperiment.jsonv2

package protocol

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"

	"github.com/dagger/dagger/dagql"
)

// MarshalJSONTo writes the merge into the encoder of what holds it, the
// same JSON MarshalJSON gives, in place: a merge's bundle is the largest
// message either side sends, and MarshalJSON's bytes were copied whole into
// the envelope's encoder.
func (m Merge) MarshalJSONTo(enc *jsontext.Encoder) error {
	bundle, err := dagql.ShareChainLayers(m.Bundle)
	if err != nil {
		return err
	}
	type merge Merge
	return jsonv2.MarshalEncode(enc, merge{Bundle: bundle, Lookup: m.Lookup})
}
