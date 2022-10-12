package schema

import (
	"encoding/json"

	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.dagger.io/dagger/core/filesystem"
	"go.dagger.io/dagger/router"
)

type coreSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &coreSchema{}

func (r *coreSchema) Name() string {
	return "core"
}

func (r *coreSchema) Schema() string {
	return `
extend type Query {
	"Core API"
	core: Core!
}

"Core API"
type Core {
	"Fetch an OCI image"
	image(ref: String!): Filesystem!
}
`
}

func (r *coreSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			r.Name(): router.PassthroughResolver,
		},
		"Core": router.ObjectResolver{
			"image": router.ToResolver(r.image),
		},
	}
}

func (r *coreSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type imageArgs struct {
	Ref string
}

func (r *coreSchema) image(ctx *router.Context, parent any, args imageArgs) (*filesystem.Filesystem, error) {
	st := llb.Image(args.Ref)
	// TODO:(sipsma) just a temporary hack to help out with issues like this while we continue to
	// flesh out the new api: https://github.com/dagger/dagger/issues/3170
	refName, err := reference.ParseNormalizedNamed(args.Ref)
	if err != nil {
		return nil, err
	}
	ref := reference.TagNameOnly(refName).String()
	_, cfgBytes, err := r.gw.ResolveImageConfig(ctx, ref, llb.ResolveImageConfigOpt{
		Platform:    &r.platform,
		ResolveMode: llb.ResolveModeDefault.String(),
	})
	if err != nil {
		return nil, err
	}
	var imgSpec specs.Image
	if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
		return nil, err
	}
	img, err := filesystem.ImageFromStateAndConfig(ctx, st, imgSpec.Config, llb.Platform(r.platform))
	if err != nil {
		return nil, err
	}
	return img.ToFilesystem()
}
