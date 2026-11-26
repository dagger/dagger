//go:build !go1.27 || !goexperiment.jsonv2

package dagql

import "encoding/json"

// countJSON returns the length of the JSON json.Marshal gives v. Before Go
// 1.27, or without json v2, it is encoded whole.
func countJSON(v any) (int64, error) {
	data, err := json.Marshal(v)
	return int64(len(data)), err
}
