package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/router"
	"github.com/dagger/graphql"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2"
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

type ProjectSDK string

const (
	ProjectSDKGo         ProjectSDK = "go"
	ProjectSDKPython     ProjectSDK = "python"
	ProjectSDKTypescript ProjectSDK = "typescript"
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
	if err := decodeID(&project, id); err != nil {
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
	if err := decodeID(&projectCommand, id); err != nil {
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
	Config ProjectConfig `json:"config"`
	// The graphql schema for the project
	Schema string `json:"schema"`
	// The project's platform
	Platform specs.Platform `json:"platform,omitempty"`
}

type ProjectConfig struct {
	Root string `json:"root"`
	Name string `json:"name"`
	SDK  string `json:"sdk,omitempty"`
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
	return encodeID[ProjectID](p)
}

func (p *Project) Clone() *Project {
	cp := *p
	if p.Directory != nil {
		cp.Directory = p.Directory.Clone()
	}
	return &cp
}

func (p *Project) Load(ctx context.Context, gw bkgw.Client, r *router.Router, progSock *Socket, pipeline pipeline.Path, source *Directory, configPath string) (*Project, error) {
	p = p.Clone()
	p.Directory = source

	configPath = p.normalizeConfigPath(configPath)
	p.ConfigPath = configPath

	configFile, err := source.File(ctx, p.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load project config at path %q: %w", configPath, err)
	}
	cfgBytes, err := configFile.Contents(ctx, gw)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config at path %q: %w", configPath, err)
	}
	if err := json.Unmarshal(cfgBytes, &p.Config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal project config: %w", err)
	}

	p.Schema, err = p.getSchema(ctx, gw, progSock, pipeline, r)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema: %w", err)
	}

	resolvers, err := p.getResolvers(ctx, gw, r, progSock, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to get resolvers: %w", err)
	}
	if err := r.Add(router.StaticSchema(router.StaticSchemaParams{
		Name:      p.Config.Name,
		Schema:    p.Schema,
		Resolvers: resolvers,
	})); err != nil {
		return nil, fmt.Errorf("failed to install project schema: %w", err)
	}
	return p, nil
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

func (p *Project) getSchema(ctx context.Context, gw bkgw.Client, progSock *Socket, pipeline pipeline.Path, r *router.Router) (string, error) {
	ctr, err := p.runtime(ctx, gw, progSock, pipeline)
	if err != nil {
		return "", fmt.Errorf("failed to get runtime container for schema: %w", err)
	}
	ctr, err = ctr.WithMountedDirectory(ctx, gw, outputMountPath, NewScratchDirectory(pipeline, p.Platform), "")
	if err != nil {
		return "", fmt.Errorf("failed to mount output directory: %w", err)
	}
	ctr, err = ctr.WithExec(ctx, gw, progSock, p.Platform, ContainerExecOpts{
		Args: []string{"-schema"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to exec schema command: %w", err)
	}
	schemaFile, err := ctr.File(ctx, gw, path.Join(outputMountPath, schemaPath))
	if err != nil {
		return "", fmt.Errorf("failed to get schema file: %w", err)
	}
	newSchema, err := schemaFile.Contents(ctx, gw)
	if err != nil {
		return "", fmt.Errorf("failed to read schema file: %w", err)
	}

	// validate it against the existing schema early
	currentSchema := r.MergedSchemas()
	_, err = gqlparser.LoadSchema(
		&ast.Source{Input: currentSchema, BuiltIn: true},
		&ast.Source{Input: string(newSchema)},
	)
	if err != nil {
		return "", fmt.Errorf("failed to parse schema: %w", err)
	}
	return string(newSchema), nil
}

func (p *Project) runtime(ctx context.Context, gw bkgw.Client, progSock *Socket, pipeline pipeline.Path) (*Container, error) {
	switch ProjectSDK(p.Config.SDK) {
	case ProjectSDKGo:
		return p.goRuntime(ctx, gw, progSock, pipeline)
	case ProjectSDKPython:
		return p.pythonRuntime(ctx, gw, progSock, pipeline)
	case ProjectSDKTypescript:
		return p.typescriptRuntime(ctx, gw, progSock, pipeline)
	default:
		return nil, fmt.Errorf("unknown sdk %q", p.Config.SDK)
	}
}

func (p *Project) getResolvers(ctx context.Context, gw bkgw.Client, r *router.Router, progSock *Socket, pipeline pipeline.Path) (router.Resolvers, error) {
	resolvers := make(router.Resolvers)
	if p.Config.SDK == "" {
		return nil, fmt.Errorf("sdk not set")
	}

	doc, err := parser.ParseSchema(&ast.Source{Input: p.Schema})
	if err != nil {
		return nil, err
	}
	for _, def := range append(doc.Definitions, doc.Extensions...) {
		if def.Kind != ast.Object {
			continue
		}
		objResolver := router.ObjectResolver{}
		resolvers[def.Name] = objResolver
		for _, field := range def.Fields {
			objResolver[field.Name], err = p.getResolver(ctx, gw, r, progSock, pipeline, field.Type)
			if err != nil {
				return nil, err
			}
		}
	}
	return resolvers, nil
}

func (p *Project) getResolver(ctx context.Context, gw bkgw.Client, r *router.Router, progSock *Socket, pipeline pipeline.Path, outputType *ast.Type) (graphql.FieldResolveFn, error) {
	ctr, err := p.runtime(ctx, gw, progSock, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container for resolver: %w", err)
	}

	return router.ToResolver(func(ctx *router.Context, parent any, args any) (any, error) {
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
		ctr, err = ctr.WithNewFile(ctx, gw, path.Join(inputMountPath, inputFile), inputBytes, 0644, "")
		if err != nil {
			return "", fmt.Errorf("failed to mount resolver input file: %w", err)
		}

		ctr, err = ctr.WithMountedDirectory(ctx, gw, outputMountPath, NewScratchDirectory(nil, p.Platform), "")
		if err != nil {
			return "", fmt.Errorf("failed to mount resolver output directory: %w", err)
		}

		ctr, err = ctr.WithExec(ctx, gw, progSock, p.Platform, ContainerExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
		if err != nil {
			return "", fmt.Errorf("failed to exec resolver: %w", err)
		}

		outputFile, err := ctr.File(ctx, gw, path.Join(outputMountPath, outputFile))
		if err != nil {
			return "", fmt.Errorf("failed to get resolver output file: %w", err)
		}
		outputBytes, err := outputFile.Contents(ctx, gw)
		if err != nil {
			return "", fmt.Errorf("failed to read resolver output file: %w", err)
		}
		var output interface{}
		if err := json.Unmarshal(outputBytes, &output); err != nil {
			return nil, fmt.Errorf("failed to unmarshal output: %w", err)
		}
		return convertOutput(output, outputType, r)
	}), nil
}

func convertOutput(output any, outputType *ast.Type, r *router.Router) (any, error) {
	if outputType.Elem != nil {
		outputType = outputType.Elem
	}

	for objectName, resolver := range r.Resolvers() {
		if objectName != outputType.Name() {
			continue
		}
		resolver, ok := resolver.(router.IDableObjectResolver)
		if !ok {
			continue
		}

		// ID-able dagger objects are serialized as their ID string across the wire
		// between the session and project container.
		outputStr, ok := output.(string)
		if !ok {
			return nil, fmt.Errorf("expected id string output for %s", objectName)
		}
		return resolver.FromID(outputStr)
	}
	return output, nil
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
	return encodeID[ProjectCommandID](p)
}
