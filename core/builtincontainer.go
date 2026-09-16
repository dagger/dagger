package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func BuiltInContainer(ctx context.Context, platform Platform, blobDigest string) (*Container, error) {
	manifestDigest, err := digest.Parse(blobDigest)
	if err != nil {
		return nil, fmt.Errorf("builtin Container manifest digest: %w", err)
	}
	container := NewContainer(platform)
	container.Lazy = &ContainerBuiltinLazy{LazyState: NewLazyState(), Platform: platform, ManifestDigest: manifestDigest}
	return container, nil
}

func builtinContainerInto(ctx context.Context, container *Container, platform Platform, manifestDigest digest.Digest) error {
	if container == nil {
		return fmt.Errorf("builtin Container lazy: nil receiver")
	}
	if container.FS != nil {
		if fs, ok := container.FS.Peek(); ok && fs != nil {
			if snap, set := fs.Snapshot.Peek(); set && snap != nil {
				return fmt.Errorf("builtin Container lazy: snapshot already installed")
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
		return fmt.Errorf("builtin Container operation manifest digest: %w", err)
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
		return nil, fmt.Errorf("builtin Container operation requires recorded _builtinContainer call")
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
		return nil, fmt.Errorf("decode builtin Container lazy: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &ContainerBuiltinLazy{LazyState: NewLazyState(), Platform: p.Platform, ManifestDigest: digest.Digest(p.ManifestDigest)}, nil
}
