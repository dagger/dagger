package resourceid

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// EncodeModule JSON marshals and base64-encodes a module ID from its typename
// and data.
func EncodeModule(typeName string, value any) (string, error) {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("failed to json marshal %T: %w", value, err)
	}
	idEnc := base64.StdEncoding.EncodeToString(jsonBytes)
	return fmt.Sprintf("moddata:%s:%s", typeName, idEnc), nil
}

// DecodeModule base64-decodes and JSON unmarshals an ID, returning the module
// typename and its data.
func DecodeModule(rest string) (string, any, error) {
	prefix, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return "", nil, fmt.Errorf("invalid id")
	}
	if prefix != "moddata" {
		return "", nil, fmt.Errorf("invalid id prefix %q", prefix)
	}

	typeName, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return "", nil, fmt.Errorf("invalid id")
	}

	jsonBytes, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode id: %w", err)
	}

	obj := map[string]any{}
	if err := json.Unmarshal(jsonBytes, &obj); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal id: %w", err)
	}
	return typeName, obj, nil
}
