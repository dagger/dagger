package common

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"cuelang.org/go/cue"
	"github.com/containerd/containerd/platforms"
	"github.com/mitchellh/go-homedir"
	buildkit "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildctl/build"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
)

// FormatValue returns the String representation of the cue value
func FormatValue(val *compiler.Value) string {
	switch {
	case val.HasAttr("artifact"):
		return "dagger.#Artifact"
	case plancontext.IsSecretValue(val):
		return "dagger.#Secret"
	case plancontext.IsFSValue(val):
		return "dagger.#FS"
	case plancontext.IsSocketValue(val):
		return "dagger.#Socket"
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

func convertRelativePaths(cacheOptionEntries []buildkit.CacheOptionsEntry) []buildkit.CacheOptionsEntry {
	pathableAttributes := []string{"src", "dest"}

	for _, option := range cacheOptionEntries {
		for _, key := range pathableAttributes {
			if _, ok := option.Attrs[key]; ok {
				path, err := homedir.Expand(option.Attrs[key])
				if err != nil {
					panic(err)
				}
				option.Attrs[key] = path
			}
		}
	}

	return cacheOptionEntries
}

func addGithubActionsAttrs(opts buildkit.CacheOptionsEntry) error {
	if opts.Type != "gha" {
		return nil
	}
	if _, ok := opts.Attrs["token"]; !ok {
		if token, ok := os.LookupEnv("ACTIONS_RUNTIME_TOKEN"); ok {
			opts.Attrs["token"] = token
		} else {
			return errors.New("missing github actions token, set \"token\" attribute in cache options or set ACTIONS_RUNTIME_TOKEN environment variable")
		}
	}
	if _, ok := opts.Attrs["url"]; !ok {
		if url, ok := os.LookupEnv("ACTIONS_CACHE_URL"); ok {
			opts.Attrs["url"] = url
		} else {
			return errors.New("missing github actions cache url, set \"url\" attribute in cache options or set ACTIONS_CACHE_URL environment variable")
		}
	}
	return nil
}

func parseExportCache(exports []string) ([]buildkit.CacheOptionsEntry, error) {
	cacheExports, err := build.ParseExportCache(exports, nil)
	if err != nil {
		return nil, err
	}

	cacheExports = convertRelativePaths(cacheExports)

	for _, ex := range cacheExports {
		if err := addGithubActionsAttrs(ex); err != nil {
			return nil, fmt.Errorf("failed to parse cache export options: %w", err)
		}
	}

	return cacheExports, nil
}

func parseImportCache(imports []string) ([]buildkit.CacheOptionsEntry, error) {
	cacheImports, err := build.ParseImportCache(imports)
	if err != nil {
		return nil, err
	}

	cacheImports = convertRelativePaths(cacheImports)

	for _, im := range cacheImports {
		if err := addGithubActionsAttrs(im); err != nil {
			return nil, fmt.Errorf("failed to parse cache import options: %w", err)
		}
	}

	return cacheImports, nil
}

// NewClient creates a new client
func NewClient(ctx context.Context) *client.Client {
	lg := log.Ctx(ctx)

	cacheExports, err := parseExportCache(viper.GetStringSlice("cache-to"))
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to parse --cache-to options")
	}

	cacheImports, err := parseImportCache(viper.GetStringSlice("cache-from"))
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to parse --cache-from options")
	}

	ep := viper.GetString("platform")
	var p *specs.Platform
	if len(ep) > 0 {
		pp, err := platforms.Parse(ep)
		if err != nil {
			lg.Fatal().Err(err).Msg("invalid value for --platform")
		}
		p = &pp
	}

	cl, err := client.New(ctx, "", client.Config{
		CacheExports:   cacheExports,
		CacheImports:   cacheImports,
		NoCache:        viper.GetBool("no-cache"),
		TargetPlatform: p,
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}

	return cl
}
