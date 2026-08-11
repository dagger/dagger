// Package enginefixture defines engine-free integration case inputs.
//
// Fixtures in this package are parsed and validated before they enter a Dagger
// graph. Multi-minute engine runs therefore observe engine behavior rather than
// discovering malformed configuration or an impossible bootstrap sequence.
package enginefixture

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	operationsSDKEntry       = "dagger-rust-sdk"
	operationsSchemaModule   = ".dagger/modules/client-schema"
	operationsSchemaConfig   = "/work/.dagger/modules/client-schema/dagger.json"
	operationsSchemaSource   = "/work/.dagger/modules/client-schema/main.go"
	operationsModuleManifest = ".dagger/modules/operations/.dagger/rust/operation-manifest.json"
	operationsClientRoot     = ".dagger/modules/client-schema/clients/rust"
)

const operationsSchemaSourceContents = `package main

type ClientSchema struct{}

func (*ClientSchema) Probe() string { return "ok" }
`

// File is one deterministic fixture file applied to the integration workspace.
type File struct {
	// Path is absolute within the case runner.
	Path string
	// Contents are validated before the plan is returned.
	Contents string
}

// OperationsPlan is the complete engine input for the operation-rendering case.
type OperationsPlan struct {
	// Files supply the already-loadable schema module used by the client renderer.
	Files []File
	// GenerateClientArgs publish only the schema module's generated-context changes.
	GenerateClientArgs []string
	// RequiredPaths are the products the engine-backed case must observe.
	RequiredPaths []string
}

type workspaceDocument struct {
	Modules map[string]workspaceModule `toml:"modules"`
}

type workspaceModule struct {
	AsSDK *workspaceSDK `toml:"as-sdk"`
}

type workspaceSDK struct {
}

type legacyModuleConfig struct {
	Name          string `json:"name"`
	EngineVersion string `json:"engineVersion"`
	SDK           struct {
		Source string `json:"source"`
	} `json:"sdk"`
	Source  string               `json:"source"`
	Clients []legacyModuleClient `json:"clients"`
}

type legacyModuleClient struct {
	Generator string `json:"generator"`
	Directory string `json:"directory"`
}

// NewOperationsPlan validates the installed workspace and constructs the case fixture.
func NewOperationsPlan(installedConfig string, engineVersion string) (OperationsPlan, error) {
	if strings.TrimSpace(engineVersion) == "" {
		return OperationsPlan{}, fmt.Errorf("operations fixture engine version is empty")
	}

	installed, err := decodeWorkspace(installedConfig)
	if err != nil {
		return OperationsPlan{}, fmt.Errorf("decode installed workspace: %w", err)
	}
	sdk, ok := installed.Modules[operationsSDKEntry]
	if !ok || sdk.AsSDK == nil {
		return OperationsPlan{}, fmt.Errorf("installed workspace omits %s as an SDK", operationsSDKEntry)
	}

	legacyConfig, err := operationsLegacyConfig(engineVersion)
	if err != nil {
		return OperationsPlan{}, err
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", operationsSchemaSourceContents, parser.AllErrors); err != nil {
		return OperationsPlan{}, fmt.Errorf("parse operations schema source: %w", err)
	}

	return OperationsPlan{
		Files: []File{
			{Path: operationsSchemaConfig, Contents: legacyConfig},
			{Path: operationsSchemaSource, Contents: operationsSchemaSourceContents},
		},
		GenerateClientArgs: []string{
			"dagger", "api", "call", "-M",
			"module-source", "--ref-string", operationsSchemaModule, "--require-kind", "LOCAL_SOURCE",
			"generated-context-changeset", "export", "--path", operationsSchemaModule,
		},
		RequiredPaths: []string{
			operationsModuleManifest,
			".dagger/modules/operations/src/dagger_generated/mod.rs",
			operationsClientRoot + "/Cargo.toml",
			operationsClientRoot + "/src/lib.rs",
			operationsClientRoot + "/src/dagger_generated/mod.rs",
			operationsClientRoot + "/.dagger/rust/operation-manifest.json",
		},
	}, nil
}

func decodeWorkspace(contents string) (workspaceDocument, error) {
	var document workspaceDocument
	if _, err := toml.Decode(contents, &document); err != nil {
		return workspaceDocument{}, err
	}
	return document, nil
}

func operationsLegacyConfig(engineVersion string) (string, error) {
	contents := fmt.Sprintf(`{
  "name": "client-schema",
  "engineVersion": %q,
  "sdk": {"source": "go"},
  "source": ".",
  "clients": [{"generator": "rust", "directory": "clients/rust"}]
}
`, engineVersion)
	var config legacyModuleConfig
	if err := json.Unmarshal([]byte(contents), &config); err != nil {
		return "", fmt.Errorf("decode operations schema config: %w", err)
	}
	if config.Name != "client-schema" || config.EngineVersion != engineVersion ||
		config.SDK.Source != "go" || config.Source != "." || len(config.Clients) != 1 ||
		config.Clients[0].Generator != "rust" || config.Clients[0].Directory != "clients/rust" {
		return "", fmt.Errorf("operations schema config does not describe the expected legacy Go module")
	}
	return contents, nil
}
