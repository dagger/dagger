//go:build !go1.27 || !goexperiment.jsonv2

package protocol

import (
	"encoding/json"
	"io"
)

// WriteJSON writes the JSON json.Marshal gives for v to w. Before Go 1.27,
// or without json v2, it is encoded whole and then written.
func WriteJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
