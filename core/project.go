package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/dagger/dagger/router"
	"github.com/dagger/graphql"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
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

type ProjectSDK string

const (
	ProjectSDKGo     ProjectSDK = "go"
	ProjectSDKPython ProjectSDK = "python"
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

func (p *Project) Load(ctx context.Context, gw bkgw.Client, r *router.Router, progSock *Socket, source *Directory, configPath string) (*Project, error) {
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

	p.Schema, err = p.getSchema(ctx, gw, r)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema: %w", err)
	}

	resolvers, err := p.getResolvers(ctx, gw, r, progSock)
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

func (p *Project) getSchema(ctx context.Context, gw bkgw.Client, r *router.Router) (string, error) {
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
		llb.Dir("/src"),
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
	newSchema := string(outputBytes)

	// validate it against the existing schema early
	currentSchema := r.MergedSchemas()
	_, err = gqlparser.LoadSchema(
		&ast.Source{Input: currentSchema, BuiltIn: true},
		&ast.Source{Input: newSchema},
	)
	if err != nil {
		return "", fmt.Errorf("failed to parse schema: %w", err)
	}
	return newSchema, nil
}

func (p *Project) runtime(ctx context.Context, gw bkgw.Client) (*Directory, error) {
	var runtimeFS *Directory
	var err error
	switch ProjectSDK(p.Config.SDK) {
	case ProjectSDKGo:
		runtimeFS, err = p.goRuntime(ctx, "/", gw, p.Platform)
	case ProjectSDKPython:
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

func (p *Project) getResolvers(ctx context.Context, gw bkgw.Client, r *router.Router, progSock *Socket) (router.Resolvers, error) {
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
			objResolver[field.Name], err = p.getResolver(ctx, gw, r, progSock, field.Type)
			if err != nil {
				return nil, err
			}
		}
	}
	return resolvers, nil
}

func (p *Project) getResolver(ctx context.Context, gw bkgw.Client, r *router.Router, progSock *Socket, outputType *ast.Type) (graphql.FieldResolveFn, error) {
	runtimeFS, err := p.runtime(ctx, gw)
	if err != nil {
		return nil, err
	}

	return router.ToResolver(func(ctx *router.Context, parent any, args any) (any, error) {
		pathArray := ctx.ResolveParams.Info.Path.AsArray()
		name := fmt.Sprintf("%+v", pathArray)

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
		input := llb.Scratch().File(llb.Mkfile(inputFile, 0644, inputBytes))

		fsState, err := runtimeFS.State()
		if err != nil {
			return nil, err
		}

		wdState, err := p.Directory.State()
		if err != nil {
			return nil, err
		}

		sid, err := progSock.ID()
		if err != nil {
			return nil, err
		}

		st := fsState.Run(
			llb.Args([]string{entrypointPath}),
			llb.Dir("/src"),
			llb.AddEnv("_DAGGER_ENABLE_NESTING", ""),
			llb.AddSSHSocket(
				llb.SSHID(sid.LLBID()),
				llb.SSHSocketTarget("/.progrock.sock"),
			),
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
