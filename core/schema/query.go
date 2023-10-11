package schema

import (
	"fmt"
	"strings"

	"github.com/blang/semver"
	"github.com/vito/progrock"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
)

type querySchema struct {
	*MergedSchemas
}

var _ ExecutableSchema = &querySchema{}

func (s *querySchema) Name() string {
	return "query"
}

func (s *querySchema) Schema() string {
	return Query
}

func (s *querySchema) Resolvers() Resolvers {
	return Resolvers{
		"JSON": jsonResolver,
		"Void": voidScalarResolver,
		"Query": ObjectResolver{
			"pipeline":                  ToResolver(s.pipeline),
			"checkVersionCompatibility": ToResolver(s.checkVersionCompatibility),
		},
		"Port": ObjectResolver{
			"protocol": ToResolver(s.portProtocolHack),
		},
	}
}

func (s *querySchema) Dependencies() []ExecutableSchema {
	return nil
}

type pipelineArgs struct {
	Name        string
	Description string
	Labels      []pipeline.Label
}

func (s *querySchema) pipeline(ctx *core.Context, parent *core.Query, args pipelineArgs) (*core.Query, error) {
	if parent == nil {
		parent = &core.Query{}
	}
	parent.Pipeline = parent.Pipeline.Add(pipeline.Pipeline{
		Name:        args.Name,
		Description: args.Description,
		Labels:      args.Labels,
	})
	return parent, nil
}

type checkVersionCompatibilityArgs struct {
	Version string
}

func (s *querySchema) checkVersionCompatibility(ctx *core.Context, _ *core.Query, args checkVersionCompatibilityArgs) (bool, error) {
	recorder := progrock.FromContext(ctx)

	// Skip development version
	if strings.Contains(engine.Version, "devel") {
		recorder.Debug("Using development engine; skipping version compatibility check.")
		return true, nil
	}

	engineVersionStr := strings.TrimPrefix(engine.Version, "v")
	engineVersion, err := semver.Parse(engineVersionStr)
	if err != nil {
		return false, fmt.Errorf("failed to parse engine version as semver: %s", err)
	}

	sdkVersionStr := strings.TrimPrefix(args.Version, "v")
	sdkVersion, err := semver.Parse(sdkVersionStr)
	if err != nil {
		return false, fmt.Errorf("failed to parse SDK version as semver: %s", err)
	}

	// If the Engine is a major version above the SDK version, fails
	// TODO: throw an error and abort the session
	if engineVersion.Major > sdkVersion.Major {
		recorder.Warn(fmt.Sprintf("Dagger engine version (%s) is significantly newer than the SDK's required version (%s). Please update your SDK.", engineVersion, sdkVersion))

		// return false, fmt.Errorf("Dagger engine version (%s) is not compatible with the SDK (%s)", engineVersion, sdkVersion)
		return false, nil
	}

	// If the Engine is older than the SDK, fails
	// TODO: throw an error and abort the session
	if engineVersion.LT(sdkVersion) {
		recorder.Warn(fmt.Sprintf("Dagger engine version (%s) is older than the SDK's required version (%s). Please update your Dagger CLI.", engineVersion, sdkVersion))

		// return false, fmt.Errorf("API version is older than the SDK, please update your Dagger CLI")
		return false, nil
	}

	// If the Engine is a minor version newer, warn
	if engineVersion.Minor > sdkVersion.Minor {
		recorder.Warn(fmt.Sprintf("Dagger engine version (%s) is newer than the SDK's required version (%s). Consider updating your SDK.", engineVersion, sdkVersion))
	}

	return true, nil
}

func (s *querySchema) portProtocolHack(ctx *core.Context, port core.Port, args any) (string, error) {
	// HACK(vito): this is a little counter-intuitive, but we need to return a
	// string instead of the core.NetworkProtocol value so the resolver layer can
	// lookup the enum value by name.
	return port.Protocol.EnumName(), nil
}
