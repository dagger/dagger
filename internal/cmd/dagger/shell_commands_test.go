package daggercmd

import (
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

func TestReasoningEffortOptions(t *testing.T) {
	values := func(opts []huh.Option[string]) []string {
		var vs []string
		for _, o := range opts {
			vs = append(vs, o.Value)
		}
		return vs
	}
	catalogModel := func(match func(llmconfig.ModelInfo) bool) string {
		t.Helper()
		for _, provider := range llmconfig.ProviderEntries() {
			for _, model := range llmconfig.ModelsForProvider(provider.ConfigKey) {
				if match(model) {
					return model.ID
				}
			}
		}
		t.Fatal("catwalk catalog has no model matching the test condition")
		return ""
	}

	// Catalog model marked non-reasoning: only "none", no invented levels.
	nonReasoning := catalogModel(func(model llmconfig.ModelInfo) bool {
		return !model.CanReason
	})
	require.Equal(t, []string{"none"}, values(reasoningEffortOptions(nonReasoning)))

	// Unknown model: conventional fallback.
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions("my-local-model")))

	// Catalog model that can reason but declares no explicit levels
	// gets the conventional fallback.
	withoutLevels := catalogModel(func(model llmconfig.ModelInfo) bool {
		return model.CanReason && len(model.ReasoningLevels) == 0
	})
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions(withoutLevels)))
}
