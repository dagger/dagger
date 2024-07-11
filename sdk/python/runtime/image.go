package main

import (
	_ "embed"
	"regexp"
	"strings"

	"github.com/distribution/reference"
)

//go:embed Dockerfile
var dockerfile string

// fromLineRegex should match: FROM <image> AS <name>
var fromLineRegex = regexp.MustCompile(`^FROM\s+([^\s]+)\s+AS\s+([^\s]+)`)

const (
	BaseImageName = "base"
	UvImageName   = "uv"
)

// Image represents a parsed docker image reference.
type Image struct {
	named reference.Named
}

// String returns the full reference.
func (i *Image) String() string {
	return i.named.String()
}

// Familiar returns the familiar string representation for the given reference.
func (i *Image) Familiar() string {
	return reference.FamiliarString(i.named)
}

// Tag returns the tag of the image reference.
func (i *Image) Tag() string {
	if tagged, ok := i.named.(reference.Tagged); ok {
		return tagged.Tag()
	}
	return ""
}

// WithTag replaces the tag in the image reference.
func (i *Image) WithTag(tag string) (*Image, error) {
	tagged, err := reference.WithTag(reference.TrimNamed(i.named), tag)
	if err != nil {
		return nil, err
	}
	return &Image{named: tagged}, nil
}

// Equal returns true if the given image reference begins with the current one.
//
// Useful to reuse a digest if name and tag are the same.
func (i *Image) Equal(full *Image) bool {
	return strings.HasPrefix(full.Familiar(), i.Familiar())
}

// parseImageRef parses a string into a named reference transforming a familiar
// name from Docker UI to a fully qualified reference.
func parseImageRef(ref string) (*Image, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return nil, err
	}
	return &Image{named: named}, nil
}

// extractImages reads from the bundled Dockerfile to extract the default docker
// image references.
func extractImages() (map[string]*Image, error) {
	lines := strings.Split(dockerfile, "\n")
	images := make(map[string]*Image)

	for _, line := range lines {
		if matches := fromLineRegex.FindStringSubmatch(strings.TrimSpace(line)); matches != nil {
			ref := matches[1]
			name := matches[2]

			image, err := parseImageRef(ref)
			if err != nil {
				return nil, err
			}

			images[name] = image
		}
	}

	return images, nil
}
