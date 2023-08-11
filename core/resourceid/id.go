package resourceid

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Encode JSON marshals and base64-encodes an arbitrary payload.
func Encode[T ~string](payload any) (T, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	idEnc := base64.StdEncoding.EncodeToString(jsonBytes)

	var t T
	t = T(fmt.Sprintf("%T:%s", t, idEnc))

	return t, nil
}

// Decode base64-decodes and JSON unmarshals an ID into an arbitrary payload.
func Decode[T ~string](payload any, id T) error {
	actualType, idEnc, ok := strings.Cut(string(id), ":")
	if !ok {
		return fmt.Errorf("malformed ID: %v", id)
	}

	desiredType := fmt.Sprintf("%T", id)
	if actualType != desiredType {
		return fmt.Errorf("ID type mismatch: %v != %v", actualType, desiredType)
	}

	jsonBytes, err := base64.StdEncoding.DecodeString(idEnc)
	if err != nil {
		return fmt.Errorf("failed to decode %T bytes: %v: %w", payload, id, err)
	}

	return json.Unmarshal(jsonBytes, payload)
}
