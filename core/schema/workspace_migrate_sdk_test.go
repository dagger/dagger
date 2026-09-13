package schema

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestPlanMigratedSDKFixups(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			// builtin SDK recorded by migration (keyed by prefixed name, bare
			// source): resolve the bare source to its real ref + name
			"dagger-php-sdk": {Source: "php"},
			"dagger-go-sdk":  {Source: "go"},
			// versioned builtin source: resolve, preserving the @version
			"dagger-java-sdk": {Source: "java@v0.18"},
			// already a full ref: leave untouched
			"custom-sdk": {Source: "github.com/dagger/go-sdk@v1.2.3"},
			// not an SDK install: ignore even with a bare source
			"plain": {Source: "mymod"},
			// local path SDK: leave untouched
			"local": {Source: "./sdks/local"},
			// bare name absent from the registry: leave untouched
			"mystery": {Source: "mystery"},
		},
		SDKs: map[string]workspace.SDKEntry{
			"php":     {Module: "dagger-php-sdk"},
			"go":      {Module: "dagger-go-sdk"},
			"java":    {Module: "dagger-java-sdk"},
			"custom":  {Module: "custom-sdk"},
			"local":   {Module: "local"},
			"mystery": {Module: "mystery"},
		},
	}

	require.Equal(t, []migratedSDKFixup{
		{ModuleName: "dagger-go-sdk", CurrentSDKName: "go", Ref: "github.com/dagger/go-sdk", SDKName: "go"},
		{ModuleName: "dagger-java-sdk", CurrentSDKName: "java", Ref: "github.com/dagger/java-sdk@v0.18", SDKName: "java"},
		{ModuleName: "dagger-php-sdk", CurrentSDKName: "php", Ref: "github.com/dagger/php-sdk", SDKName: "php"},
	}, planMigratedSDKFixups(cfg))

	require.Nil(t, planMigratedSDKFixups(nil))
}

func TestApplyMigratedSDKFixupsPreservesScopes(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"dagger-go-sdk": {Source: "go"},
		},
		SDKs: map[string]workspace.SDKEntry{
			"golang": {
				Module: "dagger-go-sdk",
				Scopes: map[string]workspace.SDKScope{
					"modules/alpine": {IsModule: true, Name: "alpine"},
					"sdk/go/client":  {Clients: []string{"."}},
				},
			},
		},
	}

	applyMigratedSDKFixups(cfg, []migratedSDKFixup{{
		ModuleName:     "dagger-go-sdk",
		CurrentSDKName: "golang",
		Ref:            "github.com/dagger/go-sdk",
		SDKName:        "go",
	}})
	require.Equal(t, "github.com/dagger/go-sdk", cfg.Modules["dagger-go-sdk"].Source)
	require.Equal(t, workspace.SDKEntry{
		Module: "dagger-go-sdk",
		Scopes: map[string]workspace.SDKScope{
			"modules/alpine": {IsModule: true, Name: "alpine"},
			"sdk/go/client":  {Clients: []string{"."}},
		},
	}, cfg.SDKs["go"])
}
