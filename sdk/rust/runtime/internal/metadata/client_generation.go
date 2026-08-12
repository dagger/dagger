// Package metadata validates the data-only Rust SDK metadata read by the Go adapter.
package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

var requiredClientGenerationFiles = [...]string{
	"**/.gitattributes",
	"**/Cargo.toml",
	"**/README.md",
	"**/rust-toolchain",
	"**/rust-toolchain.toml",
	"**/src/lib.rs",
}

// ClientGeneration is the closed metadata emitted by the Rust renderer configuration.
type ClientGeneration struct {
	FormatVersion     uint32   `json:"format_version"`
	RequiredHostFiles []string `json:"required_host_files"`
}

// DecodeClientGeneration accepts only the reviewed, ordered finite host projection.
func DecodeClientGeneration(data []byte) (ClientGeneration, error) {
	var metadata ClientGeneration
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return ClientGeneration{}, fmt.Errorf("decode client-generation metadata: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ClientGeneration{}, fmt.Errorf("decode client-generation metadata: trailing content")
	}
	if metadata.FormatVersion != 1 {
		return ClientGeneration{}, fmt.Errorf("client-generation format_version must be 1")
	}
	if len(metadata.RequiredHostFiles) != len(requiredClientGenerationFiles) {
		return ClientGeneration{}, fmt.Errorf("client-generation required host files differ from the reviewed finite set")
	}
	for index, candidate := range metadata.RequiredHostFiles {
		if !isNormalizedRelativePath(candidate) {
			return ClientGeneration{}, fmt.Errorf("client-generation required host file at index %d is invalid", index)
		}
		if candidate != requiredClientGenerationFiles[index] {
			return ClientGeneration{}, fmt.Errorf("client-generation required host files differ from the reviewed finite set")
		}
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
