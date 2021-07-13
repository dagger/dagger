package common

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"
)

func CurrentWorkspace(ctx context.Context) *state.Workspace {
	lg := log.Ctx(ctx)

	if workspacePath := viper.GetString("workspace"); workspacePath != "" {
		workspace, err := state.Open(ctx, workspacePath)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Str("path", workspacePath).
				Msg("failed to open workspace")
		}
		return workspace
	}

	workspace, err := state.Current(ctx)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to determine current workspace")
	}
	return workspace
}

func CurrentEnvironmentState(ctx context.Context, workspace *state.Workspace) *state.State {
	lg := log.Ctx(ctx)

	environmentName := viper.GetString("environment")
	if environmentName != "" {
		st, err := workspace.Get(ctx, environmentName)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to load environment")
		}
		return st
	}

	environments, err := workspace.List(ctx)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to list environments")
	}

	if len(environments) == 0 {
		lg.
			Fatal().
			Msg("no environments")
	}

	if len(environments) > 1 {
		envNames := []string{}
		for _, e := range environments {
			envNames = append(envNames, e.Name)
		}
		lg.
			Fatal().
			Err(err).
			Strs("environments", envNames).
			Msg("multiple environments available in the workspace, select one with `--environment`")
	}

	return environments[0]
}

// Re-compute an environment (equivalent to `dagger up`).
func EnvironmentUp(ctx context.Context, cl *client.Client, state *state.State, noCache bool) *environment.Environment {
	lg := log.Ctx(ctx)

	result, err := cl.Do(ctx, state, func(ctx context.Context, environment *environment.Environment, s solver.Solver) error {
		log.Ctx(ctx).Debug().Msg("bringing environment up")
		return environment.Up(ctx, s)
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to up environment")
	}
	return result
}

// FormatValue returns the String representation of the cue value
func FormatValue(val *compiler.Value) string {
	if val.HasAttr("artifact") {
		return "dagger.#Artifact"
	}
	if val.HasAttr("secret") {
		return "dagger.#Secret"
	}
	if val.IsConcreteR() != nil {
		return val.IncompleteKindString()
	}
	if val.IncompleteKindString() == "struct" {
		return "struct"
	}

	// value representation in Cue
	valStr := fmt.Sprintf("%v", val.Cue())
	// escape \n
	return strings.ReplaceAll(valStr, "\n", "\\n")
}

// ValueDocFull returns the full doc of the value
func ValueDocFull(val *compiler.Value) string {
	docs := []string{}
	for _, c := range val.Doc() {
		docs = append(docs, c.Text())
	}
	doc := strings.TrimSpace(strings.Join(docs, "\n"))
	if len(doc) == 0 {
		return "-"
	}
	return doc
}

// ValueDocOneLine returns the value doc as a single line
func ValueDocOneLine(val *compiler.Value) string {
	docs := []string{}
	for _, c := range val.Doc() {
		docs = append(docs, strings.TrimSpace(c.Text()))
	}
	doc := strings.Join(docs, " ")

	lines := strings.Split(doc, "\n")

	// Strip out FIXME, TODO, and INTERNAL comments
	docs = []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, "FIXME: ") ||
			strings.HasPrefix(line, "TODO: ") ||
			strings.HasPrefix(line, "INTERNAL: ") {
			continue
		}
		if len(line) == 0 {
			continue
		}
		docs = append(docs, line)
	}
	if len(docs) == 0 {
		return "-"
	}
	return strings.Join(docs, " ")
}

// NewClient creates a new client
func NewClient(ctx context.Context, noCache bool) *client.Client {
	lg := log.Ctx(ctx)

	cl, err := client.New(ctx, "", noCache)
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}

	return cl
}
