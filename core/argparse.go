package core

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/util/gitutil"
)

// ArgumentParser provides type-safe parsing of string arguments into Dagger objects
// This reuses the logic from cmd/dagger/flags.go but makes it available to the core package
type ArgumentParser struct {
	client       *dagger.Client
	moduleSource *dagger.ModuleSource
}

// NewArgumentParser creates a new argument parser
func NewArgumentParser(client *dagger.Client, moduleSource *dagger.ModuleSource) *ArgumentParser {
	return &ArgumentParser{
		client:       client,
		moduleSource: moduleSource,
	}
}

// ParseArgument parses a string value into the appropriate Dagger type based on the argument definition
func (p *ArgumentParser) ParseArgument(ctx context.Context, arg *FunctionArg, value string) (any, error) {
	typeName := arg.TypeDef.Type().Name()
	
	switch typeName {
	case "String":
		return value, nil
	case "Int":
		return strconv.Atoi(value)
	case "Boolean":
		return strconv.ParseBool(value)
	case "Float":
		return strconv.ParseFloat(value, 64)
	case "Directory":
		return p.parseDirectory(ctx, value, arg.Ignore)
	case "File":
		return p.parseFile(ctx, value)
	case "Secret":
		return p.parseSecret(ctx, value)
	case "Service":
		return p.parseService(ctx, value)
	case "Container":
		return p.parseContainer(ctx, value)
	case "CacheVolume":
		return p.parseCacheVolume(ctx, value)
	case "Platform":
		return p.parsePlatform(ctx, value)
	case "Socket":
		return p.parseSocket(ctx, value)
	case "ModuleSource":
		return p.parseModuleSource(ctx, value)
	case "Module":
		return p.parseModule(ctx, value)
	case "GitRepository":
		return p.parseGitRepository(ctx, value)
	case "GitRef":
		return p.parseGitRef(ctx, value)
	default:
		return nil, fmt.Errorf("unsupported argument type: %s", typeName)
	}
}

// parseSecret implements the secret parsing logic from cmd/dagger/flags.go
func (p *ArgumentParser) parseSecret(ctx context.Context, value string) (any, error) {
	if !strings.Contains(value, ":") {
		// case of e.g. `MY_ENV_SECRET`, which is shorthand for `env://MY_ENV_SECRET`
		value = "env://" + value
	}
	// legacy secrets in the form of `env:MY_ENV_SECRET` instead of `env://MY_ENV_SECRET`
	secretSource, val, _ := strings.Cut(value, ":")
	if !strings.HasPrefix(val, "//") {
		value = secretSource + "://" + val
	}

	// Handle cache key query parameter
	var cacheKey string
	sWithoutQuery, queryValsStr, ok := strings.Cut(value, "?")
	if ok && len(queryValsStr) > 0 {
		queryVals, err := url.ParseQuery(queryValsStr)
		if err != nil {
			return nil, err
		}
		if ck := queryVals.Get("cacheKey"); ck != "" {
			cacheKey = ck
			queryVals.Del("cacheKey")
			queryValsStr = queryVals.Encode()
			if len(queryValsStr) > 0 {
				value = fmt.Sprintf("%s?%s", sWithoutQuery, queryValsStr)
			} else {
				value = sWithoutQuery
			}
		}
	}

	var opts []dagger.SecretOpts
	if cacheKey != "" {
		opts = append(opts, dagger.SecretOpts{
			CacheKey: cacheKey,
		})
	}
	return p.client.Secret(value, opts...), nil
}

// parseDirectory implements the directory parsing logic from cmd/dagger/flags.go
func (p *ArgumentParser) parseDirectory(ctx context.Context, value string, ignore []string) (any, error) {
	if value == "" {
		return nil, fmt.Errorf("directory address cannot be empty")
	}

	// Try parsing as a Git URL
	gitURL, err := parseGitURL(value)
	if err == nil {
		return p.client.Directory().
			WithDirectory(
				"/",
				makeGitDirectory(gitURL, p.client),
				dagger.DirectoryWithDirectoryOpts{
					Exclude: ignore,
				}).
			Sync(ctx)
	}

	// Otherwise it's a local dir path
	path := value
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	return p.client.Host().Directory(path, dagger.HostDirectoryOpts{
		Exclude: ignore,
	}), nil
}

// parseFile implements the file parsing logic from cmd/dagger/flags.go
func (p *ArgumentParser) parseFile(ctx context.Context, value string) (any, error) {
	if value == "" {
		return nil, fmt.Errorf("file address cannot be empty")
	}

	// Try parsing as a Git URL
	gitURL, err := parseGitURL(value)
	if err == nil {
		gitDir := makeGitDirectory(gitURL, p.client)
		if gitURL.fragment != "" {
			return gitDir.File(gitURL.fragment), nil
		}
		return nil, fmt.Errorf("git URL must specify a file path in fragment")
	}

	// Otherwise it's a local file path
	path := value
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	return p.client.Host().File(path), nil
}

// parseService implements service parsing logic
func (p *ArgumentParser) parseService(ctx context.Context, value string) (any, error) {
	if value == "" {
		return nil, fmt.Errorf("service address cannot be empty")
	}

	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return nil, fmt.Errorf("invalid service address %s: %w", value, err)
	}

	portInt, err := strconv.Atoi(port)
	if err != nil {
		return nil, fmt.Errorf("invalid port %s: %w", port, err)
	}

	return p.client.Host().Service([]dagger.PortForward{{
		Frontend: portInt,
		Backend:  portInt,
		Protocol: dagger.NetworkProtocolTcp,
	}}, dagger.HostServiceOpts{
		Host: host,
	}), nil
}

// parseContainer implements container parsing logic
func (p *ArgumentParser) parseContainer(ctx context.Context, value string) (any, error) {
	return p.client.Container().From(value), nil
}

// parseCacheVolume implements cache volume parsing logic
func (p *ArgumentParser) parseCacheVolume(ctx context.Context, value string) (any, error) {
	return p.client.CacheVolume(value), nil
}

// parsePlatform implements platform parsing logic
func (p *ArgumentParser) parsePlatform(ctx context.Context, value string) (any, error) {
	platform, err := platforms.Parse(value)
	if err != nil {
		return nil, err
	}
	return dagger.Platform(platforms.Format(platform)), nil
}

// parseSocket implements socket parsing logic
func (p *ArgumentParser) parseSocket(ctx context.Context, value string) (any, error) {
	return p.client.Host().UnixSocket(value), nil
}

// parseModuleSource implements module source parsing logic
func (p *ArgumentParser) parseModuleSource(ctx context.Context, value string) (any, error) {
	return p.client.ModuleSource(value), nil
}

// parseModule implements module parsing logic
func (p *ArgumentParser) parseModule(ctx context.Context, value string) (any, error) {
	src := p.client.ModuleSource(value)
	return src.AsModule(), nil
}

// parseGitRepository implements git repository parsing logic
func (p *ArgumentParser) parseGitRepository(ctx context.Context, value string) (any, error) {
	return p.client.Git(value), nil
}

// parseGitRef implements git ref parsing logic
func (p *ArgumentParser) parseGitRef(ctx context.Context, value string) (any, error) {
	parts := strings.SplitN(value, "#", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("git ref must be in format repo#ref")
	}
	return p.client.Git(parts[0]).Ref(parts[1]), nil
}

// Helper functions (extracted from cmd/dagger/flags.go)

type gitURL struct {
	repo     string
	ref      string
	fragment string
}

func parseGitURL(address string) (*gitURL, error) {
	repo, fragment, _ := strings.Cut(address, "#")
	
	if !gitutil.IsURL(repo) {
		return nil, fmt.Errorf("not a git URL")
	}

	return &gitURL{
		repo:     repo,
		fragment: fragment,
	}, nil
}

func makeGitDirectory(gitURL *gitURL, client *dagger.Client) *dagger.Directory {
	git := client.Git(gitURL.repo)
	if gitURL.ref != "" {
		git = git.Ref(gitURL.ref)
	} else {
		git = git.Head()
	}
	return git.Tree()
}

func getLocalPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}

	// For relative paths, make them relative to the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	absPath := filepath.Join(cwd, path)
	cleanPath := pathutil.CanonicalizeLocalPath(absPath)
	
	return cleanPath, nil
}