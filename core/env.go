package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/core/environmentconfig"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

const (
	schemaPath = "/schema.graphql"

	inputMountPath = "/inputs"
	inputFile      = "/dagger.json"

	outputMountPath = "/outputs"
	outputFile      = "/dagger.json"
)

type EnvironmentID string

func (id EnvironmentID) String() string {
	return string(id)
}

func (id EnvironmentID) ToEnvironment() (*Environment, error) {
	var environment Environment
	if id == "" {
		return &environment, nil
	}
	if err := resourceid.Decode(&environment, id); err != nil {
		return nil, err
	}
	return &environment, nil
}

type EnvironmentCommandID string

func (id EnvironmentCommandID) String() string {
	return string(id)
}

func (id EnvironmentCommandID) ToEnvironmentCommand() (*EnvironmentCommand, error) {
	var environmentCommand EnvironmentCommand
	if id == "" {
		return &environmentCommand, nil
	}
	if err := resourceid.Decode(&environmentCommand, id); err != nil {
		return nil, err
	}
	return &environmentCommand, nil
}

type EnvironmentCheckID string

func (id EnvironmentCheckID) String() string {
	return string(id)
}

func (id EnvironmentCheckID) ToEnvironmentCheck() (*EnvironmentCheck, error) {
	var environmentCheck EnvironmentCheck
	if id == "" {
		return &environmentCheck, nil
	}
	if err := resourceid.Decode(&environmentCheck, id); err != nil {
		return nil, err
	}
	return &environmentCheck, nil
}

type EnvironmentCheckResultID string

func (id EnvironmentCheckResultID) String() string {
	return string(id)
}

func (id EnvironmentCheckResultID) ToEnvironmentCheckResult() (*EnvironmentCheckResult, error) {
	var environmentCheckResult EnvironmentCheckResult
	if id == "" {
		return &environmentCheckResult, nil
	}
	if err := resourceid.Decode(&environmentCheckResult, id); err != nil {
		return nil, err
	}
	return &environmentCheckResult, nil
}

type EnvironmentArtifactID string

func (id EnvironmentArtifactID) String() string {
	return string(id)
}

func (id EnvironmentArtifactID) ToEnvironmentArtifact() (*EnvironmentArtifact, error) {
	var environmentArtifact EnvironmentArtifact
	if id == "" {
		return &environmentArtifact, nil
	}
	if err := resourceid.Decode(&environmentArtifact, id); err != nil {
		return nil, err
	}
	return &environmentArtifact, nil
}

type EnvironmentShellID string

func (id EnvironmentShellID) String() string {
	return string(id)
}

func (id EnvironmentShellID) ToEnvironmentShell() (*EnvironmentShell, error) {
	var environmentShell EnvironmentShell
	if id == "" {
		return &environmentShell, nil
	}
	if err := resourceid.Decode(&environmentShell, id); err != nil {
		return nil, err
	}
	return &environmentShell, nil
}

type EnvironmentFunctionID string

func (id EnvironmentFunctionID) String() string {
	return string(id)
}

func (id EnvironmentFunctionID) ToEnvironmentFunction() (*EnvironmentFunction, error) {
	var environmentFunction EnvironmentFunction
	if id == "" {
		return &environmentFunction, nil
	}
	if err := resourceid.Decode(&environmentFunction, id); err != nil {
		return nil, err
	}
	return &environmentFunction, nil
}

type Environment struct {
	// The environment's root directory
	Directory *Directory `json:"directory"`
	// Path to the environment's config file relative to the root directory
	ConfigPath string `json:"configPath"`
	// The parsed environment config
	Config *environmentconfig.Config `json:"config"`
	// The graphql schema for the environment
	Schema string `json:"schema"`
	// The environment's platform
	Platform specs.Platform `json:"platform,omitempty"`
	// The container the environment executes in
	Runtime *Container `json:"runtime,omitempty"`
	// TODO:
	Commands  []*EnvironmentCommand  `json:"commands,omitempty"`
	Checks    []*EnvironmentCheck    `json:"checks,omitempty"`
	Artifacts []*EnvironmentArtifact `json:"artifacts,omitempty"`
	Shells    []*EnvironmentShell    `json:"shells,omitempty"`
	Functions []*EnvironmentFunction `json:"functions,omitempty"`
}

func NewEnvironment(id EnvironmentID) (*Environment, error) {
	environment, err := id.ToEnvironment()
	if err != nil {
		return nil, err
	}
	return environment, nil
}

func (env *Environment) ID() (EnvironmentID, error) {
	return resourceid.Encode[EnvironmentID](env)
}

func (env *Environment) Clone() *Environment {
	cp := *env
	if env.Directory != nil {
		cp.Directory = env.Directory.Clone()
	}
	if env.Runtime != nil {
		cp.Runtime = env.Runtime.Clone()
	}
	if env.Config != nil {
		env.Config = &environmentconfig.Config{
			Root:         env.Config.Root,
			Name:         env.Config.Name,
			SDK:          env.Config.SDK,
			Include:      cloneSlice(env.Config.Include),
			Exclude:      cloneSlice(env.Config.Exclude),
			Dependencies: cloneSlice(env.Config.Dependencies),
		}
	}
	for i, cmd := range env.Commands {
		cp.Commands[i] = cmd.Clone()
	}
	for i, check := range env.Checks {
		cp.Checks[i] = check.Clone()
	}
	for i, artifact := range env.Artifacts {
		cp.Artifacts[i] = artifact.Clone()
	}
	for i, shell := range env.Shells {
		cp.Shells[i] = shell.Clone()
	}
	for i, functions := range env.Functions {
		cp.Functions[i] = functions.Clone()
	}
	return &cp
}

type Resolver func(ctx *Context, parent any, args any) (any, error)

// Just load the config without actually getting the schema, useful for checking env metadata
// in an inexpensive way
func LoadEnvironmentConfig(
	ctx context.Context,
	bk *buildkit.Client,
	rootDir *Directory,
	configPath string,
) (*environmentconfig.Config, error) {
	configPath = normalizeConfigPath(configPath)

	configFile, err := rootDir.File(ctx, bk, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config at path %q: %w", configPath, err)
	}
	cfgBytes, err := configFile.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment config at path %q: %w", configPath, err)
	}
	var cfg environmentconfig.Config
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal environment config: %w", err)
	}
	return &cfg, nil
}

func LoadEnvironment(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	platform specs.Platform,
	rootDir *Directory,
	configPath string,
) (*Environment, error) {
	configPath = normalizeConfigPath(configPath)
	cfg, err := LoadEnvironmentConfig(ctx, bk, rootDir, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	clientMetadata.EnvironmentName = cfg.Name
	ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)

	envRuntime, err := runtime(ctx, bk, progSock, pipeline, platform, cfg.SDK, rootDir, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime container for schema: %w", err)
	}
	ctr, err := envRuntime.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(pipeline, platform), "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount output directory: %w", err)
	}

	// ask the environment for its base config (commands, etc.)
	ctr, err = ctr.WithExec(ctx, bk, progSock, platform, ContainerExecOpts{
		Args:                          []string{"-schema"},
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec schema command: %w", err)
	}
	f, err := ctr.File(ctx, bk, "/outputs/envid")
	if err != nil {
		return nil, fmt.Errorf("failed to get envid file: %w", err)
	}
	newEnvID, err := f.Contents(ctx, bk)
	if err != nil {
		return nil, fmt.Errorf("failed to read envid file: %w", err)
	}
	env, err := EnvironmentID(newEnvID).ToEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to decode envid: %w", err)
	}
	// fill in the other stuff we know about the environment
	env.Directory = rootDir
	env.ConfigPath = configPath
	env.Config = cfg
	env.Platform = platform
	env.Runtime = envRuntime
	env.Schema, err = env.buildSchema()
	if err != nil {
		return nil, fmt.Errorf("failed to build schema: %w", err)
	}

	return env, nil
}

// figure out if we were passed a path to a dagger.json file or a parent dir that may contain such a file
func normalizeConfigPath(configPath string) string {
	baseName := path.Base(configPath)
	if baseName == "dagger.json" {
		return configPath
	}
	return path.Join(configPath, "dagger.json")
}

func runtime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	platform specs.Platform,
	sdk environmentconfig.SDK,
	rootDir *Directory,
	configPath string,
) (*Container, error) {
	switch environmentconfig.SDK(sdk) {
	case environmentconfig.SDKGo:
		return goRuntime(ctx, bk, progSock, pipeline, platform, rootDir, configPath)
	case environmentconfig.SDKPython:
		return pythonRuntime(ctx, bk, progSock, pipeline, platform, rootDir, configPath)
	default:
		return nil, fmt.Errorf("unknown sdk %q", sdk)
	}
}

func (env *Environment) GQLObjectName() (string, error) {
	if env.Config.Name == "" {
		return "", fmt.Errorf("environment has no name")
	}
	// gql object name is capitalized environment name
	// TODO: enforce allowed chars (or replace unallowed w/ allowed)
	return strings.ToUpper(env.Config.Name[:1]) + env.Config.Name[1:], nil
}

func (env *Environment) buildSchema() (string, error) {
	schemaDoc := &ast.SchemaDocument{}

	objName, err := env.GQLObjectName()
	if err != nil {
		return "", fmt.Errorf("failed to get gql object name: %w", err)
	}
	obj := &ast.Definition{
		Name: objName,
		Kind: ast.Object,
	}

	// extend Query root with a field for this environment's object, which
	// will have fields for all the different entrypoints
	schemaDoc.Extensions = append(schemaDoc.Extensions, &ast.Definition{
		Name: "Query",
		Kind: ast.Object,
		Fields: ast.FieldList{&ast.FieldDefinition{
			// field is just the env name, object type is the capitalized env name (objName)
			Name: env.Config.Name,
			// TODO: Description
			Type: &ast.Type{
				NamedType: objName,
				NonNull:   true,
			},
		}},
	})

	// commands
	for _, cmd := range env.Commands {
		cmd.EnvironmentName = env.Config.Name
		if cmd.ResultType == "" {
			// TODO: this should be allowed, return type can be Void in this case
			return "", fmt.Errorf("command %q has no result type", cmd.Name)
		}
		fieldDef := &ast.FieldDefinition{
			Name:        cmd.Name,
			Description: cmd.Description,
			Type: &ast.Type{
				NamedType: cmd.ResultType,
				NonNull:   true,
			},
		}
		for _, flag := range cmd.Flags {
			fieldDef.Arguments = append(fieldDef.Arguments, &ast.ArgumentDefinition{
				Name: flag.Name,
				// Type is always string for the moment
				Type: &ast.Type{
					NamedType: "String",
					NonNull:   true,
				},
			})
		}
		obj.Fields = append(obj.Fields, fieldDef)
	}

	// checks
	for _, check := range env.Checks {
		check.EnvironmentName = env.Config.Name
		fieldDef := &ast.FieldDefinition{
			Name:        check.Name,
			Description: check.Description,
			Type: &ast.Type{
				// NOTE: the actual resolver in the environment returns a CheckResult, it will only
				// be called on to resolve the result field of the EnvironmentCheck object
				NamedType: "EnvironmentCheck",
				NonNull:   true,
			},
		}
		for _, flag := range check.Flags {
			fieldDef.Arguments = append(fieldDef.Arguments, &ast.ArgumentDefinition{
				Name: flag.Name,
				// Type is always string for the moment
				Type: &ast.Type{
					NamedType: "String",
					NonNull:   true,
				},
			})
		}
		obj.Fields = append(obj.Fields, fieldDef)
	}

	// artifacts
	for _, artifact := range env.Artifacts {
		artifact.EnvironmentName = env.Config.Name
		fieldDef := &ast.FieldDefinition{
			Name:        artifact.Name,
			Description: artifact.Description,
			Type: &ast.Type{
				NamedType: "EnvironmentArtifact",
				NonNull:   true,
			},
		}
		for _, flag := range artifact.Flags {
			fieldDef.Arguments = append(fieldDef.Arguments, &ast.ArgumentDefinition{
				Name: flag.Name,
				// Type is always string for the moment
				Type: &ast.Type{
					NamedType: "String",
					NonNull:   true,
				},
			})
		}
		obj.Fields = append(obj.Fields, fieldDef)
	}

	// shells
	for _, shell := range env.Shells {
		shell.EnvironmentName = env.Config.Name
		fieldDef := &ast.FieldDefinition{
			Name:        shell.Name,
			Description: shell.Description,
			Type: &ast.Type{
				// TODO: no hardcoding allowed
				NamedType: "Container",
				NonNull:   true,
			},
		}
		for _, flag := range shell.Flags {
			fieldDef.Arguments = append(fieldDef.Arguments, &ast.ArgumentDefinition{
				Name: flag.Name,
				// Type is always string for the moment
				Type: &ast.Type{
					NamedType: "String",
					NonNull:   true,
				},
			})
		}
		obj.Fields = append(obj.Fields, fieldDef)
	}

	// functions
	for _, function := range env.Functions {
		function.EnvironmentName = env.Config.Name
		fieldDef := &ast.FieldDefinition{
			Name:        function.Name,
			Description: function.Description,
			Type: &ast.Type{
				// TODO:
				NamedType: function.ResultType,
				NonNull:   true,
			},
		}
		for _, arg := range function.Args {
			typ := &ast.Type{
				NamedType: arg.ArgType,
				NonNull:   !arg.IsOptional,
			}
			if arg.IsList {
				typ = &ast.Type{
					Elem:    typ,
					NonNull: !arg.IsOptional,
				}
			}
			fieldDef.Arguments = append(fieldDef.Arguments, &ast.ArgumentDefinition{
				Name: arg.Name,
				Type: typ,
			})
		}
		obj.Fields = append(obj.Fields, fieldDef)
	}

	// add the fully filled-out object to the schema, compile the doc and return
	schemaDoc.Definitions = append(schemaDoc.Definitions, obj)
	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(schemaDoc)
	return buf.String(), nil
}

func (env *Environment) WithCommand(ctx context.Context, cmd *EnvironmentCommand) (*Environment, error) {
	env = env.Clone()
	if cmd.EnvironmentName == "" && env.Config != nil {
		cmd.EnvironmentName = env.Config.Name
	}
	env.Commands = append(env.Commands, cmd)
	return env, nil
}

func (env *Environment) WithShell(ctx context.Context, shell *EnvironmentShell) (*Environment, error) {
	env = env.Clone()
	if shell.EnvironmentName == "" && env.Config != nil {
		shell.EnvironmentName = env.Config.Name
	}
	env.Shells = append(env.Shells, shell)
	return env, nil
}

func (env *Environment) WithFunction(ctx context.Context, function *EnvironmentFunction) (*Environment, error) {
	env = env.Clone()
	if function.EnvironmentName == "" && env.Config != nil {
		function.EnvironmentName = env.Config.Name
	}
	env.Functions = append(env.Functions, function)
	return env, nil
}

func (env *Environment) WithCheck(ctx context.Context, check *EnvironmentCheck) (*Environment, error) {
	env = env.Clone()
	if check.EnvironmentName == "" && env.Config != nil {
		check.EnvironmentName = env.Config.Name
	}
	env.Checks = append(env.Checks, check)
	return env, nil
}

func (env *Environment) WithArtifact(ctx context.Context, artifact *EnvironmentArtifact) (*Environment, error) {
	env = env.Clone()
	if artifact.EnvironmentName == "" && env.Config != nil {
		artifact.EnvironmentName = env.Config.Name
	}
	env.Artifacts = append(env.Artifacts, artifact)
	return env, nil
}

func (env *Environment) FieldResolver(ctx context.Context, bk *buildkit.Client, progSock string, pipeline pipeline.Path) (Resolver, error) {
	return func(ctx *Context, parent any, args any) (any, error) {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		clientMetadata.EnvironmentName = env.Config.Name
		ctx.Context = engine.ContextWithClientMetadata(ctx.Context, clientMetadata)

		ctr, err := runtime(ctx, bk, progSock, pipeline, env.Platform, env.Config.SDK, env.Directory, env.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get runtime container for fieldResolver: %w", err)
		}

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
			return nil, fmt.Errorf("failed to mount resolver input file: %w", err)
		}

		ctr, err = ctr.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(nil, env.Platform), "")
		if err != nil {
			return nil, fmt.Errorf("failed to mount resolver output directory: %w", err)
		}

		ctr, err = ctr.WithExec(ctx, bk, progSock, env.Platform, ContainerExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to exec resolver: %w", err)
		}
		err = ctr.Evaluate(ctx, bk)
		if err != nil {
			return nil, fmt.Errorf("failed to exec resolver: %w", err)
		}

		outputFile, err := ctr.File(ctx, bk, path.Join(outputMountPath, outputFile))
		if err != nil {
			return nil, fmt.Errorf("failed to get resolver output file: %w", err)
		}
		outputBytes, err := outputFile.Contents(ctx, bk)
		if err != nil {
			return nil, fmt.Errorf("failed to read resolver output file: %w", err)
		}
		var output interface{}
		if err := json.Unmarshal(outputBytes, &output); err != nil {
			return nil, fmt.Errorf("failed to unmarshal output: %w", err)
		}
		return output, nil
	}, nil
}

type EnvironmentCommand struct {
	Name            string                   `json:"name"`
	Flags           []EnvironmentCommandFlag `json:"flags"`
	ResultType      string                   `json:"resultType"`
	Description     string                   `json:"description"`
	EnvironmentName string                   `json:"environmentName"`
}

type EnvironmentCommandFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SetValue    string `json:"setValue"`
}

func NewEnvironmentCommand(id EnvironmentCommandID) (*EnvironmentCommand, error) {
	environmentCmd, err := id.ToEnvironmentCommand()
	if err != nil {
		return nil, err
	}
	return environmentCmd, nil
}

func (env *EnvironmentCommand) ID() (EnvironmentCommandID, error) {
	return resourceid.Encode[EnvironmentCommandID](env)
}

func (cmd EnvironmentCommand) Clone() *EnvironmentCommand {
	cp := cmd
	cp.Flags = cloneSlice(cmd.Flags)
	return &cp
}

func (cmd *EnvironmentCommand) WithName(name string) *EnvironmentCommand {
	cmd = cmd.Clone()
	cmd.Name = name
	return cmd
}

func (cmd *EnvironmentCommand) WithFlag(flag EnvironmentCommandFlag) *EnvironmentCommand {
	cmd = cmd.Clone()
	cmd.Flags = append(cmd.Flags, flag)
	return cmd
}

func (cmd *EnvironmentCommand) WithResultType(resultType string) *EnvironmentCommand {
	cmd = cmd.Clone()
	cmd.ResultType = resultType
	return cmd
}

func (cmd *EnvironmentCommand) WithDescription(description string) *EnvironmentCommand {
	cmd = cmd.Clone()
	cmd.Description = description
	return cmd
}

func (cmd *EnvironmentCommand) SetStringFlag(name, value string) (*EnvironmentCommand, error) {
	cmd = cmd.Clone()
	for i, flag := range cmd.Flags {
		if flag.Name == name {
			cmd.Flags[i].SetValue = value
			return cmd, nil
		}
	}
	return nil, fmt.Errorf("no flag named %q", name)
}

type EnvironmentCheck struct {
	Name            string                 `json:"name"`
	Flags           []EnvironmentCheckFlag `json:"flags"`
	Description     string                 `json:"description"`
	Subchecks       []EnvironmentCheckID   `json:"subchecks"`
	EnvironmentName string                 `json:"environmentName"`
	ContainerID     ContainerID            `json:"containerID"`
}

type EnvironmentCheckFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SetValue    string `json:"setValue"`
}

type EnvironmentCheckResult struct {
	Success    bool                      `json:"success"`
	Output     string                    `json:"output"`
	Name       string                    `json:"name"`
	Subresults []*EnvironmentCheckResult `json:"subresults"`
}

func (result *EnvironmentCheckResult) ID() (EnvironmentCheckResultID, error) {
	return resourceid.Encode[EnvironmentCheckResultID](result)
}

func (result *EnvironmentCheckResult) Clone() *EnvironmentCheckResult {
	cp := *result
	cp.Subresults = cloneSlice(result.Subresults)
	return &cp
}

func NewEnvironmentCheck(id EnvironmentCheckID) (*EnvironmentCheck, error) {
	environmentCmd, err := id.ToEnvironmentCheck()
	if err != nil {
		return nil, err
	}
	return environmentCmd, nil
}

func (check *EnvironmentCheck) ID() (EnvironmentCheckID, error) {
	return resourceid.Encode[EnvironmentCheckID](check)
}

func (check *EnvironmentCheck) Digest() (digest.Digest, error) {
	return stableDigest(check)
}

func (check EnvironmentCheck) Clone() *EnvironmentCheck {
	cp := check
	cp.Flags = cloneSlice(check.Flags)
	cp.Subchecks = cloneSlice(check.Subchecks)
	return &cp
}

func (check *EnvironmentCheck) WithName(name string) *EnvironmentCheck {
	check = check.Clone()
	check.Name = name
	return check
}

func (check *EnvironmentCheck) WithFlag(flag EnvironmentCheckFlag) *EnvironmentCheck {
	check = check.Clone()
	check.Flags = append(check.Flags, flag)
	return check
}

func (check *EnvironmentCheck) WithDescription(description string) *EnvironmentCheck {
	check = check.Clone()
	check.Description = description
	return check
}

func (check *EnvironmentCheck) WithSubcheck(subcheckID EnvironmentCheckID) *EnvironmentCheck {
	check = check.Clone()
	check.Subchecks = append(check.Subchecks, subcheckID)
	return check
}

func (check *EnvironmentCheck) WithContainer(containerID ContainerID) *EnvironmentCheck {
	check = check.Clone()
	check.ContainerID = containerID
	return check
}

func (check *EnvironmentCheck) SetStringFlag(name, value string) (*EnvironmentCheck, error) {
	check = check.Clone()
	for i, flag := range check.Flags {
		if flag.Name == name {
			check.Flags[i].SetValue = value
			return check, nil
		}
	}
	return nil, fmt.Errorf("no flag named %q", name)
}

type EnvironmentArtifact struct {
	Name            string                    `json:"name"`
	Flags           []EnvironmentArtifactFlag `json:"flags"`
	Description     string                    `json:"description"`
	EnvironmentName string                    `json:"environmentName"`
	// only one of these should be set
	Container ContainerID `json:"container"`
	Directory DirectoryID `json:"directory"`
	File      FileID      `json:"file"`
}

type EnvironmentArtifactFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SetValue    string `json:"setValue"`
}

func NewEnvironmentArtifact(id EnvironmentArtifactID) (*EnvironmentArtifact, error) {
	artifact, err := id.ToEnvironmentArtifact()
	if err != nil {
		return nil, err
	}
	return artifact, nil
}

func (artifact *EnvironmentArtifact) ID() (EnvironmentArtifactID, error) {
	return resourceid.Encode[EnvironmentArtifactID](artifact)
}

func (artifact EnvironmentArtifact) Clone() *EnvironmentArtifact {
	cp := artifact
	cp.Flags = cloneSlice(artifact.Flags)
	return &cp
}

func (artifact *EnvironmentArtifact) WithName(name string) *EnvironmentArtifact {
	artifact = artifact.Clone()
	artifact.Name = name
	return artifact
}

func (artifact *EnvironmentArtifact) WithFlag(flag EnvironmentArtifactFlag) *EnvironmentArtifact {
	artifact = artifact.Clone()
	artifact.Flags = append(artifact.Flags, flag)
	return artifact
}

func (artifact *EnvironmentArtifact) WithDescription(description string) *EnvironmentArtifact {
	artifact = artifact.Clone()
	artifact.Description = description
	return artifact
}

func (artifact *EnvironmentArtifact) SetStringFlag(name, value string) (*EnvironmentArtifact, error) {
	artifact = artifact.Clone()
	for i, flag := range artifact.Flags {
		if flag.Name == name {
			artifact.Flags[i].SetValue = value
			return artifact, nil
		}
	}
	return nil, fmt.Errorf("no flag named %q", name)
}

func (artifact *EnvironmentArtifact) WithContainer(container ContainerID) *EnvironmentArtifact {
	artifact = artifact.Clone()
	artifact.Container = container
	return artifact
}

func (artifact *EnvironmentArtifact) WithDirectory(directory DirectoryID) *EnvironmentArtifact {
	artifact = artifact.Clone()
	artifact.Directory = directory
	return artifact
}

func (artifact *EnvironmentArtifact) WithFile(file FileID) *EnvironmentArtifact {
	artifact = artifact.Clone()
	artifact.File = file
	return artifact
}

func (artifact *EnvironmentArtifact) Version() (string, error) {
	switch {
	case artifact.Container != "":
		ctr, err := artifact.Container.ToContainer()
		if err != nil {
			return "", err
		}
		return ctr.Version, nil
	case artifact.Directory != "":
		dir, err := artifact.Directory.ToDirectory()
		if err != nil {
			return "", err
		}
		return dir.Version, nil
	case artifact.File != "":
		file, err := artifact.File.ToFile()
		if err != nil {
			return "", err
		}
		return file.Version, nil
	}
	return "", nil
}

func (artifact *EnvironmentArtifact) Labels() (map[string]string, error) {
	switch {
	case artifact.Container != "":
		ctr, err := artifact.Container.ToContainer()
		if err != nil {
			return nil, err
		}
		return ctr.Config.Labels, nil
	case artifact.Directory != "":
		dir, err := artifact.Directory.ToDirectory()
		if err != nil {
			return nil, err
		}
		return dir.Labels, nil
	case artifact.File != "":
		file, err := artifact.File.ToFile()
		if err != nil {
			return nil, err
		}
		return file.Labels, nil
	}
	return nil, nil
}

func (artifact *EnvironmentArtifact) SBOM() (string, error) {
	// TODO: dummy implementation
	return "", nil
}

func (artifact *EnvironmentArtifact) Export(ctx *Context, bk *buildkit.Client, path string) error {
	switch {
	case artifact.Container != "":
		ctr, err := artifact.Container.ToContainer()
		if err != nil {
			return err
		}
		return ctr.Export(ctx, bk, path, nil, "", "")
	case artifact.Directory != "":
		dir, err := artifact.Directory.ToDirectory()
		if err != nil {
			return err
		}
		return dir.Export(ctx, bk, nil, path)
	case artifact.File != "":
		file, err := artifact.File.ToFile()
		if err != nil {
			return err
		}
		return file.Export(ctx, bk, nil, path, true)
	}
	return fmt.Errorf("no artifact specified")
}

type EnvironmentShell struct {
	Name            string                 `json:"name"`
	Flags           []EnvironmentShellFlag `json:"flags"`
	Description     string                 `json:"description"`
	EnvironmentName string                 `json:"environmentName"`
}

type EnvironmentShellFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SetValue    string `json:"setValue"`
}

func NewEnvironmentShell(id EnvironmentShellID) (*EnvironmentShell, error) {
	environmentCmd, err := id.ToEnvironmentShell()
	if err != nil {
		return nil, err
	}
	return environmentCmd, nil
}

func (env *EnvironmentShell) ID() (EnvironmentShellID, error) {
	return resourceid.Encode[EnvironmentShellID](env)
}

func (cmd EnvironmentShell) Clone() *EnvironmentShell {
	cp := cmd
	cp.Flags = cloneSlice(cmd.Flags)
	return &cp
}

func (cmd *EnvironmentShell) WithName(name string) *EnvironmentShell {
	cmd = cmd.Clone()
	cmd.Name = name
	return cmd
}

func (cmd *EnvironmentShell) WithFlag(flag EnvironmentShellFlag) *EnvironmentShell {
	cmd = cmd.Clone()
	cmd.Flags = append(cmd.Flags, flag)
	return cmd
}

func (cmd *EnvironmentShell) WithDescription(description string) *EnvironmentShell {
	cmd = cmd.Clone()
	cmd.Description = description
	return cmd
}

func (cmd *EnvironmentShell) SetStringFlag(name, value string) (*EnvironmentShell, error) {
	cmd = cmd.Clone()
	for i, flag := range cmd.Flags {
		if flag.Name == name {
			cmd.Flags[i].SetValue = value
			return cmd, nil
		}
	}
	return nil, fmt.Errorf("no flag named %q", name)
}

type EnvironmentFunction struct {
	Name            string                   `json:"name"`
	Args            []EnvironmentFunctionArg `json:"args"`
	Description     string                   `json:"description"`
	ResultType      string                   `json:"resultType"`
	EnvironmentName string                   `json:"environmentName"`
}

type EnvironmentFunctionArg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ArgType     string `json:"argType"`
	IsList      bool   `json:"isList"`
	IsOptional  bool   `json:"isOptional"`
}

func NewEnvironmentFunction(id EnvironmentFunctionID) (*EnvironmentFunction, error) {
	environmentCmd, err := id.ToEnvironmentFunction()
	if err != nil {
		return nil, err
	}
	return environmentCmd, nil
}

func (env *EnvironmentFunction) ID() (EnvironmentFunctionID, error) {
	return resourceid.Encode[EnvironmentFunctionID](env)
}

func (cmd EnvironmentFunction) Clone() *EnvironmentFunction {
	cp := cmd
	cp.Args = cloneSlice(cmd.Args)
	return &cp
}

func (cmd *EnvironmentFunction) WithName(name string) *EnvironmentFunction {
	cmd = cmd.Clone()
	cmd.Name = name
	return cmd
}

func (cmd *EnvironmentFunction) WithArg(flag EnvironmentFunctionArg) *EnvironmentFunction {
	cmd = cmd.Clone()
	cmd.Args = append(cmd.Args, flag)
	return cmd
}

func (cmd *EnvironmentFunction) WithDescription(description string) *EnvironmentFunction {
	cmd = cmd.Clone()
	cmd.Description = description
	return cmd
}

func (cmd *EnvironmentFunction) WithResultType(resultType string) *EnvironmentFunction {
	cmd = cmd.Clone()
	cmd.ResultType = resultType
	return cmd
}
