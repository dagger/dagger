package schema

import (
	"context"
	"errors"
	"path"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
)

type cacheSchema struct{}

var _ SchemaResolvers = &cacheSchema{}

func (s *cacheSchema) Name() string {
	return "cache"
}

func (s *cacheSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithDynamicInputs("cacheVolume", s.cacheVolume, s.cacheVolumeCacheKey).
			IsPersistable().
			Doc("Constructs a cache volume for a given cache key.").
			Args(
				dagql.Arg("key").Doc(`A string identifier to target this cache volume (e.g., "modules-cache").`),
				dagql.Arg("source").Doc(`Identifier of the directory to use as the cache volume's root.`).
					View(AfterVersion("v0.21.0")),
				dagql.Arg("sharing").Doc(`Sharing mode of the cache volume.`).
					View(AfterVersion("v0.21.0")),
				dagql.Arg("owner").Doc(`A user:group to set for the cache volume root.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`).
					View(AfterVersion("v0.21.0")),
			),
	}.Install(srv)

	dagql.Fields[*core.CacheVolume]{}.Install(srv)
}

type cacheArgs struct {
	Key       string
	Namespace string `internal:"true" default:""`
	Source    dagql.Optional[core.DirectoryID]
	Sharing   core.CacheSharingMode `default:"SHARED"`
	Owner     string                `default:""`
}

func (s *cacheSchema) cacheVolumeCacheKey(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args cacheArgs,
	req *dagql.CallRequest,
) error {
	if args.Namespace == "" {
		m, err := parent.Self().CurrentModule(ctx)
		if err != nil && !errors.Is(err, core.ErrNoCurrentModule) {
			return err
		}
		namespaceKey, err := namespaceFromModule(ctx, m.Self())
		if err != nil {
			return err
		}
		args.Namespace = namespaceKey
		if err := req.SetArgInput(ctx, "namespace", dagql.NewString(namespaceKey), false); err != nil {
			return err
		}
	}

	return nil
}

func (s *cacheSchema) cacheVolume(ctx context.Context, parent dagql.ObjectResult[*core.Query], args cacheArgs) (dagql.Result[*core.CacheVolume], error) {
	source := dagql.Nullable[dagql.ObjectResult[*core.Directory]]{}
	if args.Source.Valid {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return dagql.Result[*core.CacheVolume]{}, err
		}
		loaded, err := args.Source.Value.Load(ctx, srv)
		if err != nil {
			return dagql.Result[*core.CacheVolume]{}, err
		}
		source = dagql.NonNull(loaded)
	}

	cache := core.NewCache(
		args.Key,
		args.Namespace,
		source,
		args.Sharing,
		args.Owner,
	)
	if err := cache.InitializeSnapshot(ctx); err != nil {
		return dagql.Result[*core.CacheVolume]{}, err
	}
	return dagql.NewResultForCurrentCall(ctx, cache)
}

func namespaceFromModule(ctx context.Context, m *core.Module) (string, error) {
	if m == nil {
		return "mainClient", nil
	}

	src := m.Source.Value
	name := src.Self().ModuleOriginalName

	var (
		symbolic string
		err      error
	)
	switch src.Self().Kind {
	case core.ModuleSourceKindLocal:
		symbolic = src.Self().SourceRootSubpath
	case core.ModuleSourceKindGit:
		symbolic = src.Self().Git.Symbolic
	case core.ModuleSourceKindDir:
		symbolic, err = directoryModuleCacheSymbolic(ctx, src.Self())
		if err != nil {
			return "", err
		}
	}

	return "mod(" + name + symbolic + ")", nil
}

// directoryModuleCacheSymbolic keeps cache volumes stable for directory-backed
// modules reconstructed from the same Git workspace. The normalized origin and
// module subpath form the logical identity; hashing keeps remote names out of
// cache metadata and prevents delimiter tricks. Plain synthetic directories
// have no trustworthy logical identity, so they retain the existing
// content-digest behavior.
func directoryModuleCacheSymbolic(ctx context.Context, src *core.ModuleSource) (string, error) {
	if src.DirSrc != nil && src.DirSrc.ContextIdentity != "" {
		subpath := path.Clean(filepath.ToSlash(src.SourceRootSubpath))
		identity := "dagger.dir-module.cache.v1\x00" + src.DirSrc.ContextIdentity + "\x00" + subpath
		return digest.FromString(identity).String(), nil
	}
	implementationDigest, err := src.SourceImplementationDigest(ctx)
	if err != nil {
		return "", err
	}
	return implementationDigest.String(), nil
}
