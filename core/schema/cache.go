package schema

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/hashutil"
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
				dagql.Arg("source").Doc(`Identifier of the directory to use as the cache volume's root.`),
				dagql.Arg("sharing").Doc(`Sharing mode of the cache volume.`),
				dagql.Arg("owner").Doc(`A user:group to set for the cache volume root.`,
					`The user and group can either be an ID (1000:1000) or a name (foo:bar).`,
					`If the group is omitted, it defaults to the same as the user.`),
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
	PrivateID string                `internal:"true" default:""`
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

	if args.Sharing == core.CacheSharingModePrivate {
		// PRIVATE is a hidden identity split, not a random value. Re-evaluating the
		// same cacheVolume call must produce the same private cache object.
		if args.PrivateID == "" {
			sourceID := ""
			if args.Source.Valid {
				encoded, err := args.Source.Value.Encode()
				if err != nil {
					return fmt.Errorf("encode private cache source ID: %w", err)
				}
				sourceID = encoded
			}
			privateID, err := privateCacheID(
				ctx,
				"cacheVolume",
				args.Key,
				args.Namespace,
				sourceID,
				string(args.Sharing),
				args.Owner,
			)
			if err != nil {
				return err
			}
			args.PrivateID = privateID
		}
		if err := req.SetArgInput(ctx, "privateID", dagql.NewString(args.PrivateID), false); err != nil {
			return err
		}
	}

	return nil
}

func (s *cacheSchema) cacheVolume(ctx context.Context, parent dagql.ObjectResult[*core.Query], args cacheArgs) (dagql.Result[*core.CacheVolume], error) {
	cache := core.NewCache(
		args.Key,
		args.Namespace,
		args.Source,
		args.Sharing,
		args.Owner,
		args.PrivateID,
	)
	if err := cache.InitializeSnapshot(ctx); err != nil {
		return dagql.Result[*core.CacheVolume]{}, err
	}
	return dagql.NewResultForCurrentCall(ctx, cache)
}

func privateCacheID(ctx context.Context, scope string, inputs ...string) (string, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	if clientMetadata.SessionID == "" {
		return "", errors.New("private cache identity requires a session ID")
	}

	hashInputs := []string{"private-cache", clientMetadata.SessionID, scope}
	hashInputs = append(hashInputs, inputs...)
	return hashutil.HashStrings(hashInputs...).String(), nil
}

func namespaceFromModule(ctx context.Context, m *core.Module) (string, error) {
	if m == nil {
		return "mainClient", nil
	}

	src := m.Source.Value
	name := src.Self().ModuleOriginalName

	var symbolic string
	switch src.Self().Kind {
	case core.ModuleSourceKindLocal:
		symbolic = src.Self().SourceRootSubpath
	case core.ModuleSourceKindGit:
		symbolic = src.Self().Git.Symbolic
	case core.ModuleSourceKindDir:
		sourceDigest, err := src.Self().SourceImplementationDigest(ctx)
		if err != nil {
			return "", err
		}
		symbolic = sourceDigest.String()
	}

	return "mod(" + name + symbolic + ")", nil
}
