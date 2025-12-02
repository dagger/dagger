package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/distribution/reference"
)

//go:embed images/base/Dockerfile
var baseDockerfile string

//go:embed images/uv/Dockerfile
var uvDockerfile string

// fromLineRegex should match: FROM <image> AS <name>
var fromLineRegex = regexp.MustCompile(`^FROM\s+([^\s]+)\s+AS\s+([^\s]+)`)

const (
	BaseImageName = "base"
	UvImageName   = "uv"
)

var baseImageNames = []string{BaseImageName, UvImageName}

// Image represents a parsed docker image reference.
type Image struct {
	named reference.Named
}

// String returns the full reference.
func (i Image) String() string {
	if i.named == nil {
		return ""
	}
	return i.named.String()
}

// Familiar returns the familiar string representation for the given reference.
func (i Image) Familiar() string {
	return reference.FamiliarString(i.named)
}

// Tag returns the tag of the image reference.
func (i Image) Tag() string {
	if tagged, ok := i.named.(reference.Tagged); ok {
		return tagged.Tag()
	}
	return ""
}

// WithTag replaces the tag in the image reference.
func (i Image) WithTag(tag string) (Image, error) {
	if i.named == nil {
		return Image{}, fmt.Errorf("empty image")
	}
	tagged, err := reference.WithTag(reference.TrimNamed(i.named), tag)
	if err != nil {
		return i, err
	}
	return Image{named: tagged}, nil
}

// Equal returns true if the given image reference begins with the current one.
//
// Useful to reuse a digest if name and tag are the same.
func (i Image) Equal(full Image) bool {
	return strings.HasPrefix(full.Familiar(), i.Familiar())
}

func (i Image) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.String())
}

func (i *Image) UnmarshalJSON(data []byte) error {
	var ref string
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	if ref == "" {
		return nil
	}
	img, err := NewImage(ref)
	if err != nil {
		return err
	}
	i.named = img.named
	return nil
}

// NewImage parses a string into a named reference transforming a familiar
// name from Docker UI to a fully qualified reference.
func NewImage(ref string) (Image, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return Image{}, err
	}
	if named == nil {
		return Image{}, fmt.Errorf("invalid image ref %q", ref)
	}
	return Image{named: named}, nil
}

// extractImages reads from the bundled Dockerfile to extract the default docker
// image references.
func extractImages() (map[string]Image, error) {
	images := make(map[string]Image)
	for _, dockerfile := range []string{baseDockerfile, uvDockerfile} {
		lines := strings.Split(dockerfile, "\n")

		for _, line := range lines {
			if matches := fromLineRegex.FindStringSubmatch(strings.TrimSpace(line)); matches != nil {
				ref := matches[1]
				name := matches[2]

				image, err := NewImage(ref)
				if err != nil {
					return nil, fmt.Errorf("parsing %q image ref: %w", name, err)
				}

				images[name] = image
			}
		}
	}

	for _, name := range baseImageNames {
		if _, found := images[name]; !found {
			return nil, fmt.Errorf("unable to find %q image ref", name)
		}
	}

	return images, nil
}
