package common

import (
	"context"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"github.com/containerd/containerd/platforms"
	"github.com/docker/buildx/util/buildflags"
	"github.com/mitchellh/go-homedir"
	buildkit "github.com/moby/buildkit/client"
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
	case plancontext.IsServiceValue(val):
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
		CacheExports:   convertRelativePaths(cacheExports),
		CacheImports:   convertRelativePaths(cacheImports),
		NoCache:        viper.GetBool("no-cache"),
		TargetPlatform: p,
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}

	return cl
}
