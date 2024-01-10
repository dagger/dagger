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
func EncodeModule(obj *ModuleObjectData) (string, error) {
	jsonBytes, err := json.Marshal(obj.Data)
	if err != nil {
		return "", fmt.Errorf("failed to json marshal %T: %w", obj.Data, err)
	}
	idEnc := base64.StdEncoding.EncodeToString(jsonBytes)
	return fmt.Sprintf("moddata:%s:%s:%s:%s",
		obj.ModDigest.Algorithm().String(),
		obj.ModDigest.Encoded(),
		obj.TypeName,
		idEnc,
	), nil
}

// DecodeModule base64-decodes and JSON unmarshals an ID, returning the module
// typename and its data.
func DecodeModuleID(id string, expectedTypeName string) (*ModuleObjectData, error) {
	prefix, rest, ok := strings.Cut(id, ":")
	if !ok {
		return nil, fmt.Errorf("invalid id")
	}
	if prefix != "moddata" {
		return nil, fmt.Errorf("invalid id prefix %q", prefix)
	}

	modDigestAlg, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, fmt.Errorf("invalid id")
	}
	modDigestStr, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, fmt.Errorf("invalid id")
	}
	modDigest, err := digest.Parse(modDigestAlg + ":" + modDigestStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse id mod digest %q: %w", modDigestStr, err)
	}

	typeName, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, fmt.Errorf("invalid id")
	}

	if expectedTypeName != "" && typeName != expectedTypeName {
		return nil, fmt.Errorf("invalid type name %q, expected %q", typeName, expectedTypeName)
	}

	jsonBytes, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return nil, fmt.Errorf("failed to decode id: %w", err)
	}

	obj := map[string]any{}
	if err := json.Unmarshal(jsonBytes, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal id: %w", err)
	}
	return &ModuleObjectData{
		Data:      obj,
		ModDigest: modDigest,
		TypeName:  typeName,
	}, nil
}

type ModuleObjectData struct {
	Data      any
	ModDigest digest.Digest
	TypeName  string
}
