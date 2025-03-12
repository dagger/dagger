package modules

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/vektah/gqlparser/v2/ast"
)

// Filename is the name of the module config file.
const Filename = "dagger.json"

// EngineVersionLatest is replaced by the current engine.Version during module init.
const EngineVersionLatest string = "latest"

func ParseModuleConfig(src []byte) (*ModuleConfigWithUserFields, error) {
	// before attempting to parse the entire config, just read the
	// engineVersion field, to perform version checks to see if it's even
	// possible
	var meta struct {
		EngineVersion string `json:"engineVersion"`
	}
	if err := json.Unmarshal(src, &meta); err != nil {
		return nil, fmt.Errorf("failed to decode module config: %w", err)
	}
	meta.EngineVersion = engine.NormalizeVersion(meta.EngineVersion)
	if !engine.CheckMaxVersionCompatibility(meta.EngineVersion, engine.BaseVersion(engine.Version)) {
		return nil, fmt.Errorf("module requires dagger %s, but you have %s", meta.EngineVersion, engine.Version)
	}

	var modCfg ModuleConfigWithUserFields
	if err := json.Unmarshal(src, &modCfg); err != nil {
		return nil, fmt.Errorf("failed to decode module config: %w", err)
	}
	return &modCfg, nil
}

// ModuleConfigWithUserFields is the config for a single module as loaded from a dagger.json file.
// Includes additional fields that should only be set by the user.
type ModuleConfigWithUserFields struct {
	ModuleConfigUserFields
	ModuleConfig
}

// ModuleConfig is the config for a single module as loaded from a dagger.json file.
// Only contains fields that are set/edited by dagger utilities.
type ModuleConfig struct {
	// The name of the module.
	Name string `json:"name"`

	// The version of the engine this module was last updated with.
	EngineVersion string `json:"engineVersion"`

	// The SDK this module uses
	SDK *SDK `json:"sdk,omitempty"`

	// Paths to explicitly include from the module, relative to the configuration file.
	Include []string `json:"include,omitempty"`

	// The modules this module depends on.
	Dependencies []*ModuleConfigDependency `json:"dependencies,omitempty"`

	// The path, relative to this config file, to the subdir containing the module's implementation source code.
	Source string `json:"source,omitempty"`

	// Codegen configuration for this module.
	Codegen *ModuleCodegenConfig `json:"codegen,omitempty"`

	// Paths to explicitly exclude from the module, relative to the configuration file.
	// Deprecated: Use !<pattern> in the include list instead.
	Exclude []string `json:"exclude,omitempty"`

	// The clients generated for this module.
	Clients []*ModuleConfigClient `json:"clients,omitempty"`
}

type ModuleConfigUserFields struct {
	// The self-describing json $schema
	Schema string `json:"$schema,omitempty"`
}

// SDK represents the sdk field in dagger.json
// The source can be reference to a built-in sdk e.g. go, php, elixir or
// can be a reference to a git path e.g. github.com/username/reponame/sdk-name
type SDK struct {
	Source string `json:"source"`
}

func (sdk *SDK) UnmarshalJSON(data []byte) error {
	if sdk == nil {
		return fmt.Errorf("cannot unmarshal into nil SDK")
	}
	if len(data) == 0 {
		sdk.Source = ""
		return nil
	}

	// check if this is a legacy config, where sdk was a string
	if data[0] == '"' {
		var sdkRefStr string
		if err := json.Unmarshal(data, &sdkRefStr); err != nil {
			return fmt.Errorf("unmarshal sdk as string: %w", err)
		}
		*sdk = SDK{Source: sdkRefStr}
		return nil
	}

	type alias SDK // lets us use the default json unmashaler
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal sdk as struct: %w", err)
	}
	*sdk = SDK(tmp)
	return nil
}

func (modCfg *ModuleConfig) UnmarshalJSON(data []byte) error {
	if modCfg == nil {
		return fmt.Errorf("cannot unmarshal into nil %T", modCfg)
	}
	if len(data) == 0 {
		return nil
	}

	type alias ModuleConfig // lets us use the default json unmashaler
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal module config: %w", err)
	}

	// Detect the case where SDK is set but Source isn't, which should only happen when loading an older config.
	// For those cases, the Source was implicitly ".", so set it to that.
	if tmp.SDK != nil && tmp.SDK.Source != "" && tmp.Source == "" {
		tmp.Source = "."
	}

	// adapt exclude to include
	for _, exclude := range tmp.Exclude {
		if len(exclude) == 0 {
			continue
		}
		if strings.HasPrefix(exclude, "!") {
			tmp.Include = append(tmp.Include, exclude[1:])
		} else {
			tmp.Include = append(tmp.Include, "!"+exclude)
		}
	}
	tmp.Exclude = nil

	*modCfg = ModuleConfig(tmp)
	return nil
}

func (modCfg *ModuleConfigWithUserFields) UnmarshalJSON(data []byte) error {
	if modCfg == nil {
		return fmt.Errorf("cannot unmarshal into nil %T", modCfg)
	}
	if len(data) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, &modCfg.ModuleConfigUserFields); err != nil {
		return fmt.Errorf("unmarshal module config: %w", err)
	}
	if err := json.Unmarshal(data, &modCfg.ModuleConfig); err != nil {
		return fmt.Errorf("unmarshal module config: %w", err)
	}
	return nil
}

func (modCfg *ModuleConfig) DependencyByName(name string) (*ModuleConfigDependency, bool) {
	for _, dep := range modCfg.Dependencies {
		if dep.Name == name {
			return dep, true
		}
	}
	return nil, false
}

type ModuleConfigDependency struct {
	// The name to use for this dependency. By default, the same as the dependency module's name,
	// but can also be overridden to use a different name.
	Name string `json:"name"`

	// The source ref of the module dependency.
	Source string `json:"source"`

	// The pinned version of the module dependency.
	Pin string `json:"pin,omitempty"`
}

func (depCfg *ModuleConfigDependency) UnmarshalJSON(data []byte) error {
	if depCfg == nil {
		return fmt.Errorf("cannot unmarshal into nil ModuleConfigDependency")
	}
	if len(data) == 0 {
		depCfg.Source = ""
		return nil
	}

	// check if this is a legacy config, where deps were just a list of strings
	if data[0] == '"' {
		var depRefStr string
		if err := json.Unmarshal(data, &depRefStr); err != nil {
			return fmt.Errorf("unmarshal module config dependency: %w", err)
		}
		*depCfg = ModuleConfigDependency{Source: depRefStr}
		return nil
	}

	type alias ModuleConfigDependency // lets us use the default json unmashaler
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal module config dependency: %w", err)
	}
	*depCfg = ModuleConfigDependency(tmp)
	return nil
}

type ModuleConfigView struct {
	Name     string   `json:"name"`
	Patterns []string `json:"patterns,omitempty"`
}

type ModuleCodegenConfig struct {
	// Whether to automatically generate a .gitignore file for this module.
	AutomaticGitignore *bool `json:"automaticGitignore,omitempty"`
}

func (cfg ModuleCodegenConfig) Clone() *ModuleCodegenConfig {
	if cfg.AutomaticGitignore == nil {
		return &cfg
	}
	clone := *cfg.AutomaticGitignore
	cfg.AutomaticGitignore = &clone
	return &cfg
}

type ModuleConfigClient struct {
	// The generator the client uses to be generated.
	Generator string `field:"true" name:"generator" json:"generator" doc:"The generator to use"`

	// The directory the client is generated in.
	Directory string `field:"true" name:"directory" json:"directory" doc:"The directory the client is generated in."`

	// Whether the client is generated in Dev mode or not.
	// If set using an official SDK like Go or Typescript, the client will use the local SDK library
	// instead of the published one.
	Dev *bool `field:"true" name:"dev" json:"dev,omitempty" doc:"If true, generate the client in developer mode."`
}

func (*ModuleConfigClient) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleConfigClient",
		NonNull:   true,
	}
}

func (*ModuleConfigClient) TypeDescription() string {
	return "The client generated for the module."
}

func (m ModuleConfigClient) Clone() *ModuleConfigClient {
	cp := m
	return &cp
}
