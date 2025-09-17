package build

import (
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/pkg/errors"
)

// ParseOCILayout parses --oci-layout
func ParseOCILayout(layouts []string) (map[string]content.Store, error) {
	contentStores := make(map[string]content.Store)
	for _, idAndDir := range layouts {
		parts := strings.SplitN(idAndDir, "=", 2)
		if len(parts) != 2 {
			return nil, errors.Errorf("oci-layout option must be 'id=path/to/layout', instead had invalid %s", idAndDir)
		}
		cs, err := local.NewStore(parts[1])
		if err != nil {
			return nil, errors.Wrapf(err, "oci-layout context at %s failed to initialize", parts[1])
		}
		contentStores[parts[0]] = cs
	}

	return contentStores, nil
}
