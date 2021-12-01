package common

import (
	"context"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"github.com/docker/buildx/util/buildflags"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/state"
)

func CurrentProject(ctx context.Context) *state.Project {
	lg := log.Ctx(ctx)

	if projectPath := viper.GetString("project"); projectPath != "" {
		project, err := state.Open(ctx, projectPath)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Str("path", projectPath).
				Msg("failed to open project")
		}
		return project
	}

	project, err := state.Current(ctx)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to determine current project")
	}
	return project
}

func CurrentEnvironmentState(ctx context.Context, project *state.Project) *state.State {
	lg := log.Ctx(ctx)

	environmentName := viper.GetString("environment")
	if environmentName != "" {
		st, err := project.Get(ctx, environmentName)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to load environment")
		}
		return st
	}

	environments, err := project.List(ctx)
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
			Msg("multiple environments available in the project, select one with `--environment`")
	}

	return environments[0]
}

// FormatValue returns the String representation of the cue value
func FormatValue(val *compiler.Value) string {
	if val.HasAttr("artifact") {
		return "dagger.#Artifact"
	}

	if val.LookupPath(cue.MakePath(
		cue.Hid("_secret", "alpha.dagger.io/dagger"),
		cue.Str("id"),
	)).Exists() {
		return "dagger.#Secret"
	}
	if val.IsConcreteR() != nil {
		return val.IncompleteKind().String()
	}
	if val.IncompleteKind() == cue.StructKind {
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
func NewClient(ctx context.Context) *client.Client {
	lg := log.Ctx(ctx)

	cacheExports, err := buildflags.ParseCacheEntry(viper.GetStringSlice("cache-to"))
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to parse --cache-to options")
	}
	cacheImports, err := buildflags.ParseCacheEntry(viper.GetStringSlice("cache-from"))
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to parse --cache-from options")
	}

	cl, err := client.New(ctx, "", client.Config{
		CacheExports: cacheExports,
		CacheImports: cacheImports,
		NoCache:      viper.GetBool("no-cache"),
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}

	return cl
}
