package core

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/util/gitutil"
)

// ServerArgumentParser provides type-safe parsing of string arguments into Dagger objects
// using the server infrastructure instead of requiring a dagger.Client
type ServerArgumentParser struct {
	query *Query
}

// NewServerArgumentParser creates a new server-side argument parser
func NewServerArgumentParser(query *Query) *ServerArgumentParser {
	return &ServerArgumentParser{
		query: query,
	}
}

// ParseArgument parses a string value into the appropriate Dagger type based on the argument definition
func (p *ServerArgumentParser) ParseArgument(ctx context.Context, arg *FunctionArg, value string) (dagql.Typed, error) {
	typeName := arg.TypeDef.Type().Name()
	
	switch typeName {
	case "String":
		return dagql.NewString(value), nil
	case "Int":
		intVal, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		return dagql.NewInt(intVal), nil
	case "Boolean":
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return nil, err
		}
		return dagql.NewBoolean(boolVal), nil
	case "Float":
		floatVal, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, err
		}
		return dagql.NewFloat(floatVal), nil
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

// parseSecret creates a secret using the server infrastructure
func (p *ServerArgumentParser) parseSecret(ctx context.Context, value string) (dagql.Typed, error) {
	if !strings.Contains(value, ":") {
		value = "env://" + value
	}
	// legacy secrets in the form of `env:MY_ENV_SECRET` instead of `env://MY_ENV_SECRET`
	secretSource, val, _ := strings.Cut(value, ":")
	if !strings.HasPrefix(val, "//") {
		value = secretSource + "://" + val
	}

	// Handle cache key query parameter (simplified for now)
	var cacheKey string
	sWithoutQuery, queryValsStr, ok := strings.Cut(value, "?")
	if ok && len(queryValsStr) > 0 {
		if strings.Contains(queryValsStr, "cacheKey=") {
			// Extract cache key (simplified parsing)
			for _, part := range strings.Split(queryValsStr, "&") {
				if strings.HasPrefix(part, "cacheKey=") {
					cacheKey = strings.TrimPrefix(part, "cacheKey=")
					value = sWithoutQuery // remove query params for now
					break
				}
			}
		}
	}

	secretStore, err := p.query.Secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret store: %w", err)
	}

	secret := &Secret{
		URI: value,
	}
	
	// Get client metadata for buildkit session ID
	clientMetadata, err := p.query.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	secret.BuildkitSessionID = clientMetadata.ClientID

	return secret, nil
}

// parseDirectory creates a directory using the server infrastructure
func (p *ServerArgumentParser) parseDirectory(ctx context.Context, value string, ignore []string) (dagql.Typed, error) {
	if value == "" {
		return nil, fmt.Errorf("directory address cannot be empty")
	}

	// Try parsing as a Git URL first
	gitURL, err := parseGitURL(value)
	if err == nil {
		// Handle git directory
		git := &GitRepository{
			Remote: gitURL.repo,
		}
		var gitRef dagql.Instance[*GitRef]
		if gitURL.ref != "" {
			gitRef = dagql.Instance[*GitRef]{Self: &GitRef{
				Repository: git,
				Name:       gitURL.ref,
			}}
		} else {
			gitRef = dagql.Instance[*GitRef]{Self: &GitRef{
				Repository: git,
				Name:       "HEAD",
			}}
		}
		
		tree := &Directory{
			GitRef: &gitRef,
		}
		
		if gitURL.fragment != "" {
			// Need to get subdirectory
			tree = &Directory{
				Parent:   tree,
				Path:     gitURL.fragment,
			}
		}
		
		return tree, nil
	}

	// Otherwise it's a local directory path
	path := value
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	dir := &Directory{
		HostPath: &HostDirectoryOpts{
			Path:    path,
			Exclude: ignore,
		},
	}
	
	return dir, nil
}

// parseFile creates a file using the server infrastructure
func (p *ServerArgumentParser) parseFile(ctx context.Context, value string) (dagql.Typed, error) {
	if value == "" {
		return nil, fmt.Errorf("file address cannot be empty")
	}

	// Try parsing as a Git URL
	gitURL, err := parseGitURL(value)
	if err == nil {
		if gitURL.fragment == "" {
			return nil, fmt.Errorf("git URL must specify a file path in fragment")
		}
		
		git := &GitRepository{
			Remote: gitURL.repo,
		}
		var gitRef dagql.Instance[*GitRef]
		if gitURL.ref != "" {
			gitRef = dagql.Instance[*GitRef]{Self: &GitRef{
				Repository: git,
				Name:       gitURL.ref,
			}}
		} else {
			gitRef = dagql.Instance[*GitRef]{Self: &GitRef{
				Repository: git,
				Name:       "HEAD",
			}}
		}
		
		file := &File{
			GitRef: &gitRef,
			Path:   gitURL.fragment,
		}
		
		return file, nil
	}

	// Otherwise it's a local file path
	path := value
	path, err = getLocalPath(path)
	if err != nil {
		return nil, err
	}

	file := &File{
		HostPath: &path,
	}
	
	return file, nil
}

// parseService creates a service using the server infrastructure
func (p *ServerArgumentParser) parseService(ctx context.Context, value string) (dagql.Typed, error) {
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

	service := &Service{
		Host: &HostServiceOpts{
			Hostname: host,
			Ports: []PortForward{{
				Frontend: portInt,
				Backend:  portInt,
				Protocol: NetworkProtocolTcp,
			}},
		},
	}
	
	return service, nil
}

// parseContainer creates a container using the server infrastructure
func (p *ServerArgumentParser) parseContainer(ctx context.Context, value string) (dagql.Typed, error) {
	container := &Container{
		ImageRef: value,
	}
	return container, nil
}

// parseCacheVolume creates a cache volume using the server infrastructure
func (p *ServerArgumentParser) parseCacheVolume(ctx context.Context, value string) (dagql.Typed, error) {
	cacheVolume := &CacheVolume{
		Key: value,
	}
	return cacheVolume, nil
}

// parsePlatform creates a platform using the server infrastructure
func (p *ServerArgumentParser) parsePlatform(ctx context.Context, value string) (dagql.Typed, error) {
	platform, err := platforms.Parse(value)
	if err != nil {
		return nil, err
	}
	return Platform(platforms.Format(platform)), nil
}

// parseSocket creates a socket using the server infrastructure
func (p *ServerArgumentParser) parseSocket(ctx context.Context, value string) (dagql.Typed, error) {
	socket := &Socket{
		HostPath: value,
	}
	return socket, nil
}

// parseModuleSource creates a module source using the server infrastructure
func (p *ServerArgumentParser) parseModuleSource(ctx context.Context, value string) (dagql.Typed, error) {
	moduleSource := &ModuleSource{
		RefString: value,
	}
	return moduleSource, nil
}

// parseModule creates a module using the server infrastructure
func (p *ServerArgumentParser) parseModule(ctx context.Context, value string) (dagql.Typed, error) {
	moduleSource := &ModuleSource{
		RefString: value,
	}
	
	module := &Module{
		Source: dagql.Instance[*ModuleSource]{Self: moduleSource},
	}
	
	return module, nil
}

// parseGitRepository creates a git repository using the server infrastructure
func (p *ServerArgumentParser) parseGitRepository(ctx context.Context, value string) (dagql.Typed, error) {
	git := &GitRepository{
		Remote: value,
	}
	return git, nil
}

// parseGitRef creates a git ref using the server infrastructure
func (p *ServerArgumentParser) parseGitRef(ctx context.Context, value string) (dagql.Typed, error) {
	parts := strings.SplitN(value, "#", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("git ref must be in format repo#ref")
	}
	
	git := &GitRepository{
		Remote: parts[0],
	}
	
	gitRef := &GitRef{
		Repository: git,
		Name:       parts[1],
	}
	
	return gitRef, nil
}

// Helper types and functions remain the same
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

func getLocalPath(path string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("path cannot be empty")
	}
	
	cleanPath := pathutil.CanonicalizeLocalPath(path)
	return cleanPath, nil
}