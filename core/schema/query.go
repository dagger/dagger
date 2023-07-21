package schema

import (
	"fmt"
	"github.com/vito/progrock"
	"strings"

	"github.com/blang/semver"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
)

type querySchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &querySchema{}

func (s *querySchema) Name() string {
	return "query"
}

func (s *querySchema) Schema() string {
	return Query
}

func (s *querySchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"pipeline":                  router.ToResolver(s.pipeline),
			"checkVersionCompatibility": router.ToResolver(s.checkVersionCompatibility),
		},
	}
}

func (s *querySchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type pipelineArgs struct {
	Name        string
	Description string
	Labels      []pipeline.Label
}

func (s *querySchema) pipeline(ctx *router.Context, parent *core.Query, args pipelineArgs) (*core.Query, error) {
	if parent == nil {
		parent = &core.Query{}
	}
	parent.Context.Pipeline = parent.Context.Pipeline.Add(pipeline.Pipeline{
		Name:        args.Name,
		Description: args.Description,
		Labels:      args.Labels,
	})
	return parent, nil
}

type checkVersionCompatibilityArgs struct {
	Version string
}

func (s *querySchema) checkVersionCompatibility(ctx *router.Context, _ *core.Query, args checkVersionCompatibilityArgs) (bool, error) {
	recorder := progrock.RecorderFromContext(ctx)

	// Skip development version
	if strings.Contains(engine.Version, "devel") {
		recorder.Warn("Using Engine in development version, skip version compatibility check.")

		return true, nil
	}

	engineVersion, err := semver.Parse(engine.Version)
	if err != nil {
		recorder.Error(fmt.Sprintf("Could not compare engine and SDK version, they might be incompatible!"))

		// TODO: throw an error and abort the session
		// return false, err
		return true, nil
	}

	sdkVersion, err := semver.Parse(args.Version)
	if err != nil {
		recorder.Error(fmt.Sprintf("Could not compare engine and SDK version, they might be incompatible!"))

		// TODO: throw an error and abort the session
		// return false, err
		return true, nil
	}

	// If the Engine is a major version above the SDK version, fails
	// TODO: throw an error and abort the session
	if engineVersion.Major > sdkVersion.Major {
		recorder.Warn(fmt.Sprintf("Dagger engine version (%s) is not compatible with the SDK (%s), please update your SDK.", engineVersion, sdkVersion))

		// return false, fmt.Errorf("Dagger engine version (%s) is not compatible with the SDK (%s)", engineVersion, sdkVersion)
		return true, nil
	}

	// If the Engine is older than the SDK, fails
	// TODO: throw an error and abort the session
	if engineVersion.LT(sdkVersion) {
		recorder.Warn(fmt.Sprintf("Dagger engine version (%s) is older than the SDK (%s), please update your Dagger CLI.", engineVersion, sdkVersion))

		// return false, fmt.Errorf("API version is older than the SDK, please update your Dagger CLI")
		return true, nil
	}

	// If the Engine is a minor version newer, warn
	if engineVersion.Minor > sdkVersion.Minor {
		recorder.Warn(fmt.Sprintf("API (%s) and SDK (%s) versions mismatchs.", engineVersion, sdkVersion))
	}

	return true, nil
}
