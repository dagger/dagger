package task

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mitchellh/go-homedir"
	buildkit "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildctl/build"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("CacheConfig", func() Task { return &cacheConfigTask{} })
}

type cacheConfigTask struct {
}

func (t cacheConfigTask) Run(ctx context.Context, pctx *plancontext.Context, _ *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	// TODO: this could probably be cleaner by making a struct and using .Decode

	// imports
	if imports := v.Lookup("imports"); imports.Exists() {
		importFields, err := v.Lookup("imports").Fields()
		if err != nil {
			return nil, fmt.Errorf("failed to lookup cache imports: %w", err)
		}
		var importConfigStrs []string
		for _, importField := range importFields {
			importField := importField.Value
			typ, err := importField.Lookup("type").String()
			if err != nil {
				return nil, fmt.Errorf("failed to lookup cache import type: %w", err)
			}
			configStr := "type=" + typ
			switch typ {
			case "gha":
				if scopeField := importField.Lookup("scope"); scopeField.Exists() {
					var err error
					scope, err := scopeField.String()
					if err != nil {
						return nil, fmt.Errorf("failed to lookup GHA cache import scope: %w", err)
					}
					configStr += ",scope=" + scope
				}

				if urlVal := v.Lookup("url"); urlVal.Exists() {
					var err error
					url, err := urlVal.String()
					if err != nil {
						return nil, fmt.Errorf("failed to lookup GHA cache import url: %w", err)
					}
					configStr += ",url=" + url
				}

				// TODO: this should be a secret, not string
				if tokenVal := v.Lookup("token"); tokenVal.Exists() {
					var err error
					token, err := tokenVal.String()
					if err != nil {
						return nil, fmt.Errorf("failed to lookup GHA cache import token: %w", err)
					}
					configStr += ",token=" + token
				}

			case "registry":
				ref, err := v.Lookup("ref").String()
				if err != nil {
					return nil, fmt.Errorf("failed to lookup registry cache import ref: %w", err)
				}
				configStr += ",ref=" + ref
			default:
				return nil, fmt.Errorf("unsupported cache import type: %s", typ)
			}
			importConfigStrs = append(importConfigStrs, configStr)
		}

		// TODO: remove or lower to debug
		lg.Info().Msgf("import cache config: %+v", importConfigStrs)

		importOpts, err := parseImportCache(importConfigStrs)
		if err != nil {
			return nil, fmt.Errorf("failed to parse cache import config: %w", err)
		}
		pctx.CacheConfig.SetImports(importOpts)
	}

	// exports
	if exportField := v.Lookup("export"); exportField.Exists() {
		typ, err := exportField.Lookup("type").String()
		if err != nil {
			return nil, fmt.Errorf("failed to lookup cache export type: %w", err)
		}
		exportConfigStr := "type=" + typ
		switch typ {
		case "gha":
			if scopeField := exportField.Lookup("scope"); scopeField.Exists() {
				var err error
				scope, err := scopeField.String()
				if err != nil {
					return nil, fmt.Errorf("failed to lookup GHA cache export scope: %w", err)
				}
				exportConfigStr += ",scope=" + scope
			}

			if urlVal := v.Lookup("url"); urlVal.Exists() {
				var err error
				url, err := urlVal.String()
				if err != nil {
					return nil, fmt.Errorf("failed to lookup GHA cache export url: %w", err)
				}
				exportConfigStr += ",url=" + url
			}

			// TODO: this should be a secret, not string
			if tokenVal := v.Lookup("token"); tokenVal.Exists() {
				var err error
				token, err := tokenVal.String()
				if err != nil {
					return nil, fmt.Errorf("failed to lookup GHA cache export token: %w", err)
				}
				exportConfigStr += ",token=" + token
			}

		case "registry":
			ref, err := v.Lookup("ref").String()
			if err != nil {
				return nil, fmt.Errorf("failed to lookup registry cache export ref: %w", err)
			}
			exportConfigStr += ",ref=" + ref
		default:
			return nil, fmt.Errorf("unsupported cache export type: %s", typ)
		}

		mode, err := exportField.Lookup("mode").String()
		if err != nil {
			return nil, fmt.Errorf("failed to lookup cache export mode: %w", err)
		}
		exportConfigStr += ",mode=" + mode

		// TODO: remove or lower to debug
		lg.Info().Msgf("export cache config: %s", exportConfigStr)

		exportOpts, err := parseExportCache([]string{exportConfigStr})
		if err != nil {
			return nil, fmt.Errorf("failed to parse cache export config: %w", err)
		}
		pctx.CacheConfig.SetExports(exportOpts)
	}

	return compiler.NewValue(), nil
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

// TODO: not sure if reading env vars from here is the right place... probably deprecate this behavior and use client env explicitly
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
