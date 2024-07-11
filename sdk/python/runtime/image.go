package main

import (
	_ "embed"
	"regexp"
	"strings"

	"github.com/distribution/reference"
)

//go:embed Dockerfile
var dockerfile string

// Should match: FROM <image> as <name>
var fromLineRegex = regexp.MustCompile(`^FROM\s+([^\s]+)\s+AS\s+([^\s]+)`)

const (
	BaseImageName = "base"
	UvImageName   = "uv"
)

// Image represents a parsed docker image reference.
type Image struct {
	named reference.Named
}

func (i *Image) String() string {
	return i.named.String()
}

func (i *Image) Familiar() string {
	return reference.FamiliarString(i.named)
}

func (i *Image) Tag() string {
	if tagged, ok := i.named.(reference.Tagged); ok {
		return tagged.Tag()
	}
	return ""
}

// Compare compares two image references, excluding the digest.
func (i *Image) WithTag(tag string) (*Image, error) {
	tagged, err := reference.WithTag(reference.TrimNamed(i.named), tag)
	if err != nil {
		return nil, err
	}
	return &Image{named: tagged}, nil
}

func (i *Image) Equal(full *Image) bool {
	return strings.HasPrefix(full.Familiar(), i.Familiar())
}

func parseImageRef(ref string) (*Image, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return nil, err
	}
	return &Image{named: named}, nil
}

// Function to extract the components from the Dockerfile contents and populate the map
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
