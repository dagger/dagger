package dagql

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// partAddressKeyByEncoder is partAddressKey as it was: the path's check, then
// json.Marshal of the whole address.
func partAddressKeyByEncoder(address PersistedPartAddress) (string, error) {
	if address.Part == "" {
		return "", fmt.Errorf("part address: empty part")
	}
	if _, err := canonicalPath(address.OutputPath); err != nil {
		return "", err
	}
	data, err := json.Marshal(address)
	return string(data), err
}

// A part address's key is byte for byte what json.Marshal makes of the
// address, and an invalid one fails alike: for every byte as a part's name,
// alone and between plain characters, for names in UTF-8, invalid UTF-8 and
// the characters json escapes, and for each with no path, an empty path,
// and paths of fields and indexes, valid and not.
func TestPartAddressKeyIsItsJSONEncoding(t *testing.T) {
	t.Parallel()
	var parts []PartKey
	for b := range 256 {
		parts = append(parts, PartKey([]byte{byte(b)}), PartKey("fs"+string([]byte{byte(b)})+"x"))
	}
	parts = append(parts, "", "fs", "metadata", "execMeta", "a/b:c.d-e_f", "é", "日本", " ", " ", "\xff\xfe", "a\"b", `a\b`, "<>&", "\x7f", " ", "tab\tx")
	paths := []PersistedRefPath{
		nil,
		{},
		{{Field: "dir"}},
		{{Field: "items"}, {Index: 2, IsIndex: true}},
		{{Index: 0, IsIndex: true}},
		{{Field: ""}},
		{{Index: -1, IsIndex: true}},
		{{Field: "a", Index: 1}},
		{{Field: "<é>"}},
	}
	for _, path := range paths {
		for _, part := range parts {
			address := PersistedPartAddress{OutputPath: path, Part: part}
			want, wantErr := partAddressKeyByEncoder(address)
			got, gotErr := partAddressKey(address)
			require.Equal(t, wantErr == nil, gotErr == nil, "%#v: %v, then %v", address, wantErr, gotErr)
			if wantErr != nil {
				require.Equal(t, wantErr.Error(), gotErr.Error(), "%#v", address)
				continue
			}
			require.Equal(t, want, got, "%#v", address)
		}
	}
}
