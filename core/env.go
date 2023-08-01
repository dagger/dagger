package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/dagger/dagger/core/environmentconfig"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
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

type Environment struct {
	// The environment's root directory
	Directory *Directory `json:"directory"`
	// Path to the environment's config file relative to the root directory
	ConfigPath string `json:"configPath"`
	// The parsed environment config
	Config environmentconfig.Config `json:"config"`
	// The graphql schema for the environment
	Schema string `json:"schema"`
	// The environment's platform
	Platform specs.Platform `json:"platform,omitempty"`
	// TODO:
	Commands []*EnvironmentCommand `json:"commands,omitempty"`
	Checks   []*EnvironmentCheck   `json:"checks,omitempty"`
	Shells   []*EnvironmentShell   `json:"shells,omitempty"`
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
	for i, cmd := range env.Commands {
		cp.Commands[i] = cmd.Clone()
	}
	for i, check := range env.Checks {
		cp.Checks[i] = check.Clone()
	}
	return &cp
}

type Resolver func(ctx *Context, parent any, args any) (any, error)

func LoadEnvironment(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	platform specs.Platform,
	rootDir *Directory,
	configPath string,
) (*Environment, Resolver, error) {
	configPath = normalizeConfigPath(configPath)

	configFile, err := rootDir.File(ctx, bk, configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load environment config at path %q: %w", configPath, err)
	}
	cfgBytes, err := configFile.Contents(ctx, bk)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read environment config at path %q: %w", configPath, err)
	}
	var cfg environmentconfig.Config
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal environment config: %w", err)
	}

	ctr, err := runtime(ctx, bk, progSock, pipeline, platform, cfg.SDK, rootDir, configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get runtime container for schema: %w", err)
	}
	ctr, err = ctr.WithMountedDirectory(ctx, bk, outputMountPath, NewScratchDirectory(pipeline, platform), "")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to mount output directory: %w", err)
	}

	// ask the environment for its base config (commands, etc.)
	ctr, err = ctr.WithExec(ctx, bk, progSock, platform, ContainerExecOpts{
		Args:                          []string{"-schema"},
		ExperimentalPrivilegedNesting: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to exec schema command: %w", err)
	}
	f, err := ctr.File(ctx, bk, "/outputs/envid")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get envid file: %w", err)
	}
	newEnvID, err := f.Contents(ctx, bk)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read envid file: %w", err)
	}
	env, err := EnvironmentID(newEnvID).ToEnvironment()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode envid: %w", err)
	}
	// fill in the other stuff we know about the environment
	env.Directory = rootDir
	env.ConfigPath = configPath
	env.Config = cfg
	env.Platform = platform

	resolver, err := env.resolver(ctx, bk, progSock, pipeline)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get resolver: %w", err)
	}
	return env, resolver, nil
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

func (env *Environment) WithCommand(ctx context.Context, cmd *EnvironmentCommand) (*Environment, error) {
	env = env.Clone()
	if cmd.ResultType == "" {
		// TODO: this should be allowed, return type can be Void in this case
		return nil, fmt.Errorf("command %q has no result type", cmd.Name)
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

	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(&ast.SchemaDocument{
		Extensions: ast.DefinitionList{
			&ast.Definition{
				// TODO: we need some namespace
				// TODO:
				// Name:   "Extensions",
				Name:   "Query",
				Kind:   ast.Object,
				Fields: ast.FieldList{fieldDef},
			},
		},
	})
	env.Schema = env.Schema + "\n" + buf.String()

	env.Commands = append(env.Commands, cmd)
	return env, nil
}

func (env *Environment) WithCheck(ctx context.Context, check *EnvironmentCheck) (*Environment, error) {
	env = env.Clone()

	fieldDef := &ast.FieldDefinition{
		Name:        check.Name,
		Description: check.Description,
		Type: &ast.Type{
			NamedType: "EnvironmentCheckResult",
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

	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(&ast.SchemaDocument{
		Extensions: ast.DefinitionList{
			&ast.Definition{
				// TODO: we need some namespace
				// TODO:
				// Name:   "Extensions",
				Name:   "Query",
				Kind:   ast.Object,
				Fields: ast.FieldList{fieldDef},
			},
		},
	})
	env.Schema = env.Schema + "\n" + buf.String()

	checkID, err := check.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get check id: %w", err)
	}
	for i, subcheckID := range check.Subchecks {
		subcheck, err := NewEnvironmentCheck(subcheckID)
		if err != nil {
			return nil, fmt.Errorf("failed to get subcheck %q: %w", subcheckID, err)
		}
		subcheck.ParentCheck = checkID
		newSubcheckID, err := subcheck.ID()
		if err != nil {
			return nil, fmt.Errorf("failed to get subcheck id: %w", err)
		}
		check.Subchecks[i] = newSubcheckID
		env, err = env.WithCheck(ctx, subcheck)
		if err != nil {
			return nil, err
		}
	}

	if check.ParentCheck == "" {
		// only include top-level checks in the environment's check list
		env.Checks = append(env.Checks, check)
	}
	return env, nil
}

func (env *Environment) WithShell(ctx context.Context, shell *EnvironmentShell) (*Environment, error) {
	env = env.Clone()
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

	buf := &bytes.Buffer{}
	formatter.NewFormatter(buf).FormatSchemaDocument(&ast.SchemaDocument{
		Extensions: ast.DefinitionList{
			&ast.Definition{
				// TODO: we need some namespace
				// TODO:
				// Name:   "Extensions",
				Name:   "Query",
				Kind:   ast.Object,
				Fields: ast.FieldList{fieldDef},
			},
		},
	})
	env.Schema = env.Schema + "\n" + buf.String()

	env.Shells = append(env.Shells, shell)
	return env, nil
}

func (env *Environment) resolver(ctx context.Context, bk *buildkit.Client, progSock string, pipeline pipeline.Path) (Resolver, error) {
	return func(ctx *Context, parent any, args any) (any, error) {
		ctr, err := runtime(ctx, bk, progSock, pipeline, env.Platform, env.Config.SDK, env.Directory, env.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get runtime container for resolver: %w", err)
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
	Name        string                   `json:"name"`
	Flags       []EnvironmentCommandFlag `json:"flags"`
	ResultType  string                   `json:"resultType"`
	Description string                   `json:"description"`
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
	Name        string                 `json:"name"`
	Flags       []EnvironmentCheckFlag `json:"flags"`
	Description string                 `json:"description"`
	Subchecks   []EnvironmentCheckID   `json:"subchecks"`
	ParentCheck EnvironmentCheckID     `json:"parent_check"`
}

type EnvironmentCheckFlag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SetValue    string `json:"setValue"`
}

type EnvironmentCheckResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Name    string `json:"name"`

	ParentCheck EnvironmentCheckID `json:"parent_check"`
}

func NewEnvironmentCheck(id EnvironmentCheckID) (*EnvironmentCheck, error) {
	environmentCmd, err := id.ToEnvironmentCheck()
	if err != nil {
		return nil, err
	}
	return environmentCmd, nil
}

func (env *EnvironmentCheck) ID() (EnvironmentCheckID, error) {
	return resourceid.Encode[EnvironmentCheckID](env)
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

func (check *EnvironmentCheck) WithSubcheck(subcheck *EnvironmentCheck) (*EnvironmentCheck, error) {
	check = check.Clone()
	var err error
	subcheck.ParentCheck, err = check.ID()
	if err != nil {
		return nil, err
	}
	subcheckID, err := subcheck.ID()
	if err != nil {
		return nil, err
	}
	check.Subchecks = append(check.Subchecks, subcheckID)
	return check, nil
}

type EnvironmentShell struct {
	Name        string                 `json:"name"`
	Flags       []EnvironmentShellFlag `json:"flags"`
	Description string                 `json:"description"`
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
