package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/projectconfig"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const (
	schemaPath = "/schema.graphql"

	inputMountPath = "/inputs"
	inputFile      = "/dagger.json"

	outputMountPath = "/outputs"
	outputFile      = "/dagger.json"
)

type ProjectID string

func (id ProjectID) String() string {
	return string(id)
}

func (id ProjectID) ToProject() (*Project, error) {
	var project Project
	if id == "" {
		return &project, nil
	}
	if err := resourceid.Decode(&project, id); err != nil {
		return nil, err
	}
	return &project, nil
}

type ProjectCommandID string

func (id ProjectCommandID) String() string {
	return string(id)
}

func (id ProjectCommandID) ToProjectCommand() (*ProjectCommand, error) {
	var projectCommand ProjectCommand
	if id == "" {
		return &projectCommand, nil
	}
	if err := resourceid.Decode(&projectCommand, id); err != nil {
		return nil, err
	}
	return &projectCommand, nil
}

type Project struct {
	// The project's root directory
	Directory *Directory `json:"directory"`
	// Path to the project's config file relative to the root directory
	ConfigPath string `json:"configPath"`
	// The parsed project config
	Config projectconfig.Config `json:"config"`
	// The graphql schema for the project
	Schema string `json:"schema"`
	// The project's platform
	Platform specs.Platform `json:"platform,omitempty"`
}

func NewProject(id ProjectID, platform specs.Platform) (*Project, error) {
	project, err := id.ToProject()
	if err != nil {
		return nil, err
	}
	project.Platform = platform
	return project, nil
}

func (p *Project) ID() (ProjectID, error) {
	return resourceid.Encode[ProjectID](p)
}

func (p *Project) Clone() *Project {
	cp := *p
	if p.Directory != nil {
		cp.Directory = p.Directory.Clone()
	}
	return &cp
}

type Resolver func(ctx *Context, parent any, args any) (any, error)

func (p *Project) Load(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	source *Directory,
	configPath string,
) (*Project, Resolver, error) {
	p = p.Clone()
	p.Directory = source

	configPath = p.normalizeConfigPath(configPath)
	p.ConfigPath = configPath

	configFile, err := source.File(ctx, bk, p.ConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load project config at path %q: %w", configPath, err)
	}
	cfgBytes, err := configFile.Contents(ctx, bk)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read project config at path %q: %w", configPath, err)
	}
	if err := json.Unmarshal(cfgBytes, &p.Config); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal project config: %w", err)
	}

	p.Schema, err = p.getSchema(ctx, bk, progSock, pipeline)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get schema: %w", err)
	}

	resolver, err := p.getResolver(ctx, bk, progSock, pipeline)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get resolvers: %w", err)
	}
	return p, resolver, nil
}

func (p *Project) Commands(ctx context.Context) ([]ProjectCommand, error) {
	schema, err := parser.ParseSchema(&ast.Source{Input: p.Schema})
	if err != nil {
		return nil, fmt.Errorf("failed to parse schema: %w", err)
	}

	schemaTypes := make(map[string]*ast.Definition)
	for _, def := range schema.Definitions {
		schemaTypes[def.Name] = def
	}

	queryExtDef := schema.Extensions.ForName("Query")
	if queryExtDef == nil {
		return nil, fmt.Errorf("schema is missing Query extension")
	}
	fields := queryExtDef.Fields

	commands := make([]ProjectCommand, 0, len(fields))
	for _, field := range fields {
		if field.Name == "__schema" || field.Name == "__type" {
			continue
		}
		cmd, err := p.schemaToCommand(field, schemaTypes)
		if err != nil {
			return nil, fmt.Errorf("failed to convert schema to command: %w", err)
		}
		commands = append(commands, *cmd)
	}

	return commands, nil
}

// figure out if we were passed a path to a dagger.json file or a parent dir that may contain such a file
func (p *Project) normalizeConfigPath(configPath string) string {
	baseName := path.Base(configPath)
	if baseName == "dagger.json" {
		return configPath
	}
	return path.Join(configPath, "dagger.json")
}

func (p *Project) schemaToCommand(field *ast.FieldDefinition, schemaTypes map[string]*ast.Definition) (*ProjectCommand, error) {
	cmd := ProjectCommand{
		Name:        field.Name,
		Description: field.Description,
	}

	// args become flags
	for _, arg := range field.Arguments {
		flag := ProjectCommandFlag{
			Name:        arg.Name,
			Description: arg.Description,
		}
		cmd.Flags = append(cmd.Flags, flag)
	}

	// subfield objects become subcommands
	returnType := field.Type
	if returnType.Elem != nil {
		returnType = returnType.Elem
	}
	cmd.ResultType = returnType.Name()
	returnObj, ok := schemaTypes[returnType.Name()]
	if ok {
		for _, subfield := range returnObj.Fields {
			subcmd, err := p.schemaToCommand(subfield, schemaTypes)
			if err != nil {
				return nil, err
			}
			cmd.Subcommands = append(cmd.Subcommands, *subcmd)
		}
	}
	return &cmd, nil
}

func (p *Project) getSchema(ctx context.Context, bk *buildkit.Client, progSock string, pipeline pipeline.Path) (string, error) {
	ctr, err := p.runtime(ctx, bk, progSock, pipeline)
	if err != nil {
		return "", fmt.Errorf("failed to get runtime container for schema: %w", err)
	}
	ctr, err = ctr.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(pipeline, p.Platform), "")
	if err != nil {
		return "", fmt.Errorf("failed to mount output directory: %w", err)
	}
	ctr, err = ctr.WithExec(ctx, bk, progSock, p.Platform, ContainerExecOpts{
		Args: []string{"-schema"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to exec schema command: %w", err)
	}
	schemaFile, err := ctr.File(ctx, bk, path.Join(outputMountPath, schemaPath))
	if err != nil {
		return "", fmt.Errorf("failed to get schema file: %w", err)
	}
	newSchema, err := schemaFile.Contents(ctx, bk)
	if err != nil {
		return "", fmt.Errorf("failed to read schema file: %w", err)
	}
	return string(newSchema), nil
}

func (p *Project) runtime(ctx context.Context, bk *buildkit.Client, progSock string, pipeline pipeline.Path) (*Container, error) {
	switch projectconfig.SDK(p.Config.SDK) {
	case projectconfig.SDKGo:
		return p.goRuntime(ctx, bk, progSock, pipeline)
	case projectconfig.SDKPython:
		return p.pythonRuntime(ctx, bk, progSock, pipeline)
	default:
		return nil, fmt.Errorf("unknown sdk %q", p.Config.SDK)
	}
}

func (p *Project) getResolver(ctx context.Context, bk *buildkit.Client, progSock string, pipeline pipeline.Path) (Resolver, error) {
	ctr, err := p.runtime(ctx, bk, progSock, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container for resolver: %w", err)
	}

	return func(ctx *Context, parent any, args any) (any, error) {
		resolverName := fmt.Sprintf("%s.%s", ctx.ResolveParams.Info.ParentType.Name(), ctx.ResolveParams.Info.FieldName)

		inputMap := map[string]any{
			"resolver": resolverName,
			"args":     args,
			"parent":   parent,
		}
		inputBytes, err := json.Marshal(inputMap)
		if err != nil {
			return nil, err
		}
		ctr, err = ctr.WithNewFile(ctx, bk, path.Join(inputMountPath, inputFile), inputBytes, 0644, "")
		if err != nil {
			return "", fmt.Errorf("failed to mount resolver input file: %w", err)
		}

		ctr, err = ctr.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(nil, p.Platform), "")
		if err != nil {
			return "", fmt.Errorf("failed to mount resolver output directory: %w", err)
		}

		ctr, err = ctr.WithExec(ctx, bk, progSock, p.Platform, ContainerExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
		if err != nil {
			return "", fmt.Errorf("failed to exec resolver: %w", err)
		}

		outputFile, err := ctr.File(ctx, bk, path.Join(outputMountPath, outputFile))
		if err != nil {
			return "", fmt.Errorf("failed to get resolver output file: %w", err)
		}
		outputBytes, err := outputFile.Contents(ctx, bk)
		if err != nil {
			return "", fmt.Errorf("failed to read resolver output file: %w", err)
		}
		var output interface{}
		if err := json.Unmarshal(outputBytes, &output); err != nil {
			return nil, fmt.Errorf("failed to unmarshal output: %w", err)
		}
		return output, nil
	}, nil
}

type ProjectCommand struct {
	Name        string               `json:"name"`
	Flags       []ProjectCommandFlag `json:"flags"`
	ResultType  string               `json:"resultType"`
	Description string               `json:"description"`
	Subcommands []ProjectCommand     `json:"subcommands"`
}

type ProjectCommandFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func NewProjectCommand(id ProjectCommandID) (*ProjectCommand, error) {
	projectCmd, err := id.ToProjectCommand()
	if err != nil {
		return nil, err
	}
	return projectCmd, nil
}

func (p *ProjectCommand) ID() (ProjectCommandID, error) {
	return resourceid.Encode[ProjectCommandID](p)
}
