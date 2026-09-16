package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func BuiltInContainer(ctx context.Context, platform Platform, blobDigest string) (*Container, error) {
	container := NewContainer(platform)
	manifestDigest := digest.Digest(blobDigest)
	if err := builtinContainerInto(ctx, container, platform, manifestDigest); err != nil {
		return nil, errors.Join(err, container.OnRelease(context.WithoutCancel(ctx)))
	}
	if err := recordCompletedBuiltinProducer(container, &ContainerBuiltinLazy{LazyState: NewLazyState(), Platform: platform, ManifestDigest: manifestDigest}); err != nil {
		return nil, errors.Join(err, container.OnRelease(context.WithoutCancel(ctx)))
	}
	return container, nil
}

func recordCompletedBuiltinProducer(container *Container, producer *ContainerBuiltinLazy) error {
	if container == nil || producer == nil {
		return fmt.Errorf("record builtin Container producer: nil value or producer")
	}
	if container.Lazy != nil || container.completedRecipe != nil || len(container.completedRecipeJSON) != 0 {
		return fmt.Errorf("record builtin Container producer: operation already recorded")
	}
	container.completedRecipe = producer
	return nil
}

func builtinContainerInto(ctx context.Context, container *Container, platform Platform, manifestDigest digest.Digest) error {
	if container == nil {
		return fmt.Errorf("builtin Container producer: nil receiver")
	}
	if container.FS != nil {
		if fs, ok := container.FS.Peek(); ok && fs != nil {
			if snap, set := fs.Snapshot.Peek(); set && snap != nil {
				return fmt.Errorf("builtin Container producer: snapshot already installed")
			}
		}
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	info, err := query.BuiltinOCIStore().Info(ctx, manifestDigest)
	if err != nil {
		return fmt.Errorf("lookup builtin image manifest %s: %w", manifestDigest, err)
	}
	manifestDesc := specs.Descriptor{Digest: manifestDigest, Size: info.Size, MediaType: specs.MediaTypeImageManifest}
	if err := contentutil.CopyChain(ctx, query.OCIStore(), query.BuiltinOCIStore(), manifestDesc); err != nil {
		return fmt.Errorf("copy builtin image content: %w", err)
	}
	container.Platform = platform
	_, err = container.FromOCIStore(ctx, manifestDesc, "")
	return err
}

type ContainerBuiltinLazy struct {
	LazyState
	Platform       Platform
	ManifestDigest digest.Digest
}

type persistedContainerBuiltinLazy struct {
	Platform       Platform `json:"platform"`
	ManifestDigest string   `json:"manifestDigest"`
}

func (p *persistedContainerBuiltinLazy) validate() error {
	if err := digest.Digest(p.ManifestDigest).Validate(); err != nil {
		return fmt.Errorf("builtin Container producer manifest digest: %w", err)
	}
	return nil
}

func (lazy *ContainerBuiltinLazy) Evaluate(ctx context.Context, container *Container) error {
	return lazy.LazyState.Evaluate(ctx, "Query._builtinContainer", func(ctx context.Context) error {
		if err := (&persistedContainerBuiltinLazy{ManifestDigest: lazy.ManifestDigest.String()}).validate(); err != nil {
			return err
		}
		if err := builtinContainerInto(ctx, container, lazy.Platform, lazy.ManifestDigest); err != nil {
			return err
		}
		return nil
	})
}

func (*ContainerBuiltinLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (lazy *ContainerBuiltinLazy) EncodePersisted(ctx context.Context, enc *dagql.PersistEncodeContext) (json.RawMessage, error) {
	if enc.Call() == nil || enc.Call().Field != "_builtinContainer" {
		return nil, fmt.Errorf("builtin Container producer requires recorded _builtinContainer call")
	}
	p := persistedContainerBuiltinLazy{Platform: lazy.Platform, ManifestDigest: lazy.ManifestDigest.String()}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func decodeContainerBuiltinLazy(payload json.RawMessage) (Lazy[*Container], error) {
	var p persistedContainerBuiltinLazy
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("decode builtin Container producer: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &ContainerBuiltinLazy{LazyState: NewLazyState(), Platform: p.Platform, ManifestDigest: digest.Digest(p.ManifestDigest)}, nil
}
