package schema

import (
	"fmt"
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

func (s *querySchema) checkVersionCompatibility(_ *router.Context, _ *core.Query, args checkVersionCompatibilityArgs) (bool, error) {
	// Skip development version
	if strings.Contains(engine.Version, "devel") {
		return true, nil
	}

	apiVersion, err := semver.Parse(engine.Version)
	if err != nil {
		return false, err
	}

	sdkVersion, err := semver.Parse(args.Version)
	if err != nil {
		return false, err
	}

	// If the API is a major  or minor version newer, fails
	if apiVersion.Major > sdkVersion.Major {
		return false, fmt.Errorf("API version is not compatible with the SDK, please update your SDK version")
	}

	// If API is older, fails
	if apiVersion.LT(sdkVersion) {
		return false, fmt.Errorf("API version is older than the SDK, please update your CLI")
	}

	// If the API is a minor version newer, warn
	if apiVersion.Minor > sdkVersion.Minor {
		// TODO display a warning using progrock
		fmt.Println("Warning: API and SDK versions mismatch")
	}

	return true, nil
}
