package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

var engineDefaultPlatformInput = dagql.ImplicitInput{
	Name: core.EngineDefaultPlatformInput,
	Resolver: func(ctx context.Context, _ map[string]dagql.Input) (dagql.Input, error) {
		return currentEngineDefaultPlatform(ctx)
	},
}

func currentEngineDefaultPlatform(ctx context.Context) (core.Platform, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return core.Platform{}, err
	}
	return query.Platform(), nil
}

type platformSchema struct{}

var _ SchemaResolvers = &platformSchema{}

func (s *platformSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("defaultPlatform", s.defaultPlatform).
			WithInput(engineDefaultPlatformInput).
			Doc(`The default platform of the engine.`),
	}.Install(srv)

	srv.InstallScalar(core.Platform{})
}

func (s *platformSchema) defaultPlatform(ctx context.Context, parent *core.Query, _ struct{}) (core.Platform, error) {
	return parent.Platform(), nil
}

// withPlatformDoc documents Directory.withPlatform and File.withPlatform.
func withPlatformDoc(object string) []string {
	return []string{
		`Retrieves this ` + object + `, declared as made for the given platform.`,
		`Declare a result whose contents are the same whichever platform made
		them, such as a Go package compiled for a platform by any platform's Go
		toolchain. An engine of another platform that makes the same ` + object + `
		for the same platform then finds this one, and the other way around.`,
		`The contents are not checked: a declared result that depends on the
		platform it was made on can be found where it doesn't belong.`,
	}
}

type withPlatformArgs struct {
	Platform core.Platform
}

// withPlatformDynamicInputs identifies a result declared as made for a
// platform by its recipe without the engine's platform
// (dagql.Cache.PlatformDigest), as its content, before lookup, so that an
// engine of another platform that made it for the same platform hits.
func withPlatformDynamicInputs[T dagql.Typed](ctx context.Context, parent dagql.ObjectResult[T], args withPlatformArgs, req *dagql.CallRequest) error {
	frame, err := parent.ResultCall()
	if err != nil {
		return err
	}
	dig, err := core.DeclaredPlatformDigest(ctx, frame, args.Platform)
	if err != nil {
		return err
	}
	req.ExtraDigests = append(req.ExtraDigests,
		call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: dig},
		call.ExtraDigest{Label: dagql.ExtraDigestLabelPlatform, Digest: dig},
	)
	return nil
}
