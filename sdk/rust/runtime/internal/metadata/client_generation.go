// Package metadata validates the data-only Rust SDK metadata read by the Go adapter.
package metadata

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// ClientGeneration is the closed metadata emitted by the Rust renderer configuration.
type ClientGeneration struct {
	FormatVersion     uint32   `json:"format_version"`
	RequiredHostFiles []string `json:"required_host_files"`
}

// DecodeClientGeneration rejects alternate versions, unknown fields, duplicate paths,
// and any path which cannot be confined beneath a host generation root.
func DecodeClientGeneration(data []byte) (ClientGeneration, error) {
	var metadata ClientGeneration
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return ClientGeneration{}, fmt.Errorf("decode client-generation metadata: %w", err)
	}
	if metadata.FormatVersion != 1 {
		return ClientGeneration{}, fmt.Errorf("client-generation format_version must be 1")
	}
	seen := make(map[string]struct{}, len(metadata.RequiredHostFiles))
	for _, candidate := range metadata.RequiredHostFiles {
		if !isNormalizedRelativePath(candidate) {
			return ClientGeneration{}, fmt.Errorf("required host file %q is not a normalized relative path", candidate)
		}
		if _, exists := seen[candidate]; exists {
			return ClientGeneration{}, fmt.Errorf("required host file %q occurs more than once", candidate)
		}
		seen[candidate] = struct{}{}
	}
	return metadata, nil
}

func isNormalizedRelativePath(candidate string) bool {
	return candidate != "" &&
		!strings.HasPrefix(candidate, "/") &&
		!strings.ContainsAny(candidate, "\\:") &&
		!strings.Contains(candidate, "//") &&
		path.Clean(candidate) == candidate &&
		candidate != "." && candidate != ".." &&
		!strings.HasPrefix(candidate, "../")
}
