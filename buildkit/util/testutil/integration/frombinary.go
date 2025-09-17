package integration

import (
	"context"
	"encoding/json"
	"os"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/images/archive"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func providerFromBinary(fn string) (_ ocispecs.Descriptor, _ content.Provider, _ func(), err error) {
	ctx := context.TODO()

	tmpDir, err := os.MkdirTemp("", "buildkit-state")
	if err != nil {
		return ocispecs.Descriptor{}, nil, nil, err
	}
	close := func() {
		os.RemoveAll(tmpDir)
	}
	defer func() {
		if err != nil {
			close()
		}
	}()
	// can't use contentutil.Buffer because ImportIndex takes content.Store even though only requires Provider/Ingester
	c, err := local.NewStore(tmpDir)
	if err != nil {
		return ocispecs.Descriptor{}, nil, nil, err
	}

	f, err := os.Open(fn)
	if err != nil {
		return ocispecs.Descriptor{}, nil, nil, err
	}
	defer f.Close()

	desc, err := archive.ImportIndex(ctx, c, f)
	if err != nil {
		return ocispecs.Descriptor{}, nil, nil, err
	}

	var idx ocispecs.Index
	dt, err := content.ReadBlob(ctx, c, desc)
	if err != nil {
		return ocispecs.Descriptor{}, nil, nil, err
	}
	if err := json.Unmarshal(dt, &idx); err != nil {
		return ocispecs.Descriptor{}, nil, nil, err
	}

	return idx.Manifests[0], c, close, nil
}
