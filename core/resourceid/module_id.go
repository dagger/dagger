package resourceid

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"
)

// EncodeModule JSON marshals and base64-encodes a module ID from its typename
// and data.
func EncodeModule(modDigest digest.Digest, typeName string, value any) (string, error) {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("failed to json marshal %T: %w", value, err)
	}
	idEnc := base64.StdEncoding.EncodeToString(jsonBytes)
	return fmt.Sprintf("moddata:%s:%s:%s", modDigest.Encoded(), typeName, idEnc), nil
}

// DecodeModule base64-decodes and JSON unmarshals an ID, returning the module
// typename and its data.
// TODO: too many returns, return struct? also accept in in EncodeModule too if so
func DecodeModuleID(id string, expectedTypeName string) (any, digest.Digest, string, error) {
	prefix, rest, ok := strings.Cut(id, ":")
	if !ok {
		return nil, "", "", fmt.Errorf("invalid id")
	}
	if prefix != "moddata" {
		return nil, "", "", fmt.Errorf("invalid id prefix %q", prefix)
	}

	modDigestStr, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, "", "", fmt.Errorf("invalid id")
	}
	// TODO: no need to assume canonical I suppose, can just parse alg too...
	modDigest, err := digest.Parse(digest.Canonical.String() + ":" + modDigestStr)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to parse id mod digest %q: %w", modDigestStr, err)
	}

	typeName, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, "", "", fmt.Errorf("invalid id")
	}

	// TODO: expectedTypeName is as an arg is kinda ugly now, maybe separate func for it?
	if expectedTypeName != "" && typeName != expectedTypeName {
		return nil, "", "", fmt.Errorf("invalid type name %q, expected %q", typeName, expectedTypeName)
	}

	jsonBytes, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to decode id: %w", err)
	}

	obj := map[string]any{}
	if err := json.Unmarshal(jsonBytes, &obj); err != nil {
		return nil, "", "", fmt.Errorf("failed to unmarshal id: %w", err)
	}
	return obj, modDigest, typeName, nil
}
