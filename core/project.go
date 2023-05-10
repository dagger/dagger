package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/router"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/language/ast"
	"github.com/dagger/graphql/language/parser"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2"
	gqlparserast "github.com/vektah/gqlparser/v2/ast"
)

const (
	schemaPath     = "/schema.graphql"
	entrypointPath = "/entrypoint"

	tmpMountPath = "/tmp"

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
	Directory  *Directory     `json:"directory"`
	ConfigPath string         `json:"configPath"`
	Config     ProjectConfig  `json:"config"`
	Schema     string         `json:"schema"`
	Platform   specs.Platform `json:"platform,omitempty"`
}

type ProjectConfig struct {
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

func (p *Project) Load(ctx context.Context, gw bkgw.Client, r *router.Router, source *Directory, configPath string) (*Project, error) {
	p = p.Clone()
	p.Directory = source
	p.ConfigPath = configPath

	configFile, err := source.File(ctx, configPath)
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

	p.Schema, err = p.getSchema(ctx, gw)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema: %w", err)
	}

	resolvers, err := p.getResolvers(ctx, gw)
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
	schema, err := gqlparser.LoadSchema(&gqlparserast.Source{
		Input: p.Schema,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse schema: %w", err)
	}

	var commands []ProjectCommand
	for _, field := range schema.Query.Fields {
		if field.Name == "__schema" || field.Name == "__type" {
			continue
		}
		cmd, err := p.schemaToCommand(field, schema)
		if err != nil {
			return nil, fmt.Errorf("failed to convert schema to command: %w", err)
		}
		commands = append(commands, *cmd)
	}

	return commands, nil
}

func (p *Project) schemaToCommand(field *gqlparserast.FieldDefinition, schema *gqlparserast.Schema) (*ProjectCommand, error) {
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
	returnObj, ok := schema.Types[returnType.Name()]
	if ok {
		for _, subfield := range returnObj.Fields {
			subcmd, err := p.schemaToCommand(subfield, schema)
			if err != nil {
				return nil, err
			}
			cmd.Subcommands = append(cmd.Subcommands, *subcmd)
		}
	}
	return &cmd, nil
}

func (p *Project) getSchema(ctx context.Context, gw bkgw.Client) (string, error) {
	runtimeFS, err := p.runtime(ctx, gw)
	if err != nil {
		return "", fmt.Errorf("failed to get runtime filesystem: %w", err)
	}

	fsState, err := runtimeFS.State()
	if err != nil {
		return "", fmt.Errorf("failed to get runtime filesystem state: %w", err)
	}

	projectDirState, err := p.Directory.State()
	if err != nil {
		return "", fmt.Errorf("failed to get project dir state: %w", err)
	}

	st := fsState.Run(
		llb.Args([]string{entrypointPath, "-schema"}),
		llb.AddMount("/src", projectDirState, llb.Readonly),
		llb.ReadonlyRootFS(),
	)
	outputMnt := st.AddMount(outputMountPath, llb.Scratch())
	outputDef, err := outputMnt.Marshal(ctx, llb.Platform(p.Platform))
	if err != nil {
		return "", fmt.Errorf("failed to marshal output mount: %w", err)
	}
	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: outputDef.ToPB(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to solve output mount: %w", err)
	}
	ref, err := res.SingleRef()
	if err != nil {
		return "", fmt.Errorf("failed to get output mount ref: %w", err)
	}
	outputBytes, err := ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: "/schema.graphql",
	})
	if err != nil {
		return "", fmt.Errorf("failed to read schema: %w", err)
	}
	return string(outputBytes), nil
}

func (p *Project) runtime(ctx context.Context, gw bkgw.Client) (*Directory, error) {
	var runtimeFS *Directory
	var err error
	switch p.Config.SDK {
	case "go":
		runtimeFS, err = p.goRuntime(ctx, "/", gw, p.Platform)
	case "python":
		runtimeFS, err = p.pythonRuntime(ctx, "/", gw, p.Platform)
	default:
		return nil, fmt.Errorf("unknown sdk %q", p.Config.SDK)
	}
	if err != nil {
		return nil, err
	}
	if _, err := runtimeFS.Stat(ctx, gw, "."); err != nil {
		return nil, err
	}
	return runtimeFS, nil
}

func (p *Project) getResolvers(ctx context.Context, gw bkgw.Client) (router.Resolvers, error) {
	resolvers := make(router.Resolvers)
	if p.Config.SDK == "" {
		return nil, fmt.Errorf("sdk not set")
	}

	doc, err := parser.Parse(parser.ParseParams{Source: p.Schema})
	if err != nil {
		return nil, err
	}
	for _, def := range doc.Definitions {
		var obj *ast.ObjectDefinition

		if def, ok := def.(*ast.ObjectDefinition); ok {
			obj = def
		}

		if def, ok := def.(*ast.TypeExtensionDefinition); ok {
			obj = def.Definition
		}

		if obj == nil {
			continue
		}

		objResolver := router.ObjectResolver{}
		resolvers[obj.Name.Value] = objResolver
		for _, field := range obj.Fields {
			objResolver[field.Name.Value], err = p.getResolver(ctx, gw)
			if err != nil {
				return nil, err
			}
		}
	}
	return resolvers, nil
}

func (p *Project) getResolver(ctx context.Context, gw bkgw.Client) (graphql.FieldResolveFn, error) {
	runtimeFS, err := p.runtime(ctx, gw)
	if err != nil {
		return nil, err
	}

	return router.ToResolver(func(ctx *router.Context, parent any, args any) (any, error) {
		pathArray := ctx.ResolveParams.Info.Path.AsArray()
		name := fmt.Sprintf("%+v", pathArray)

		resolverName := fmt.Sprintf("%s.%s", ctx.ResolveParams.Info.ParentType.Name(), ctx.ResolveParams.Info.FieldName)
		inputMap := map[string]interface{}{
			"resolver": resolverName,
			"args":     args,
			"parent":   parent,
		}
		inputBytes, err := json.Marshal(inputMap)
		if err != nil {
			return nil, err
		}
		input := llb.Scratch().File(llb.Mkfile(inputFile, 0644, inputBytes))

		fsState, err := runtimeFS.State()
		if err != nil {
			return nil, err
		}

		wdState, err := p.Directory.State()
		if err != nil {
			return nil, err
		}

		st := fsState.Run(
			llb.Args([]string{entrypointPath}),
			llb.AddEnv("_DAGGER_ENABLE_NESTING", ""),
			// make extensions compatible with the shim, in future we can actually enable retrieval of stdout/stderr
			llb.AddMount("/.dagger_meta_mount", llb.Scratch(), llb.Tmpfs()),
			llb.AddMount(inputMountPath, input, llb.Readonly),
			llb.AddMount(tmpMountPath, llb.Scratch(), llb.Tmpfs()),
		)

		if p.Config.SDK == "go" {
			st.AddMount("/src", wdState, llb.Readonly) // TODO: not actually needed here, just makes go server code easier at moment
		}

		outputMnt := st.AddMount(outputMountPath, llb.Scratch())
		outputDef, err := outputMnt.Marshal(ctx, llb.Platform(p.Platform), llb.WithCustomName(name))
		if err != nil {
			return nil, err
		}

		res, err := gw.Solve(ctx, bkgw.SolveRequest{
			Definition: outputDef.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}
		outputBytes, err := ref.ReadFile(ctx, bkgw.ReadRequest{
			Filename: outputFile,
		})
		if err != nil {
			return nil, err
		}
		var output interface{}
		if err := json.Unmarshal(outputBytes, &output); err != nil {
			return nil, fmt.Errorf("failed to unmarshal output: %w", err)
		}
		return output, nil
	}), nil
}

type ProjectCommand struct {
	Name        string
	Flags       []ProjectCommandFlag
	Description string
	Subcommands []ProjectCommand
}

type ProjectCommandFlag struct {
	Name        string
	Description string
}

func NewProjectCommand(id ProjectCommandID) (*ProjectCommand, error) {
	project, err := id.ToProjectCommand()
	if err != nil {
		return nil, err
	}
	return project, nil
}

func (p *ProjectCommand) ID() (ProjectCommandID, error) {
	return encodeID[ProjectCommandID](p)
}
