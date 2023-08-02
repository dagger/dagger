package resourceid

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Encode JSON marshals and base64-encodes an arbitrary payload.
func Encode[T ~string](payload any) (T, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)

	return T(b64Bytes), nil
}

// Decode base64-decodes and JSON unmarshals an ID into an arbitrary payload.
func Decode[T ~string](payload any, id T) error {
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(id)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(id))
	if err != nil {
		return fmt.Errorf("failed to decode %T bytes: %v: %w", payload, id, err)
	}

	jsonBytes = jsonBytes[:n]

	return json.Unmarshal(jsonBytes, payload)
}
