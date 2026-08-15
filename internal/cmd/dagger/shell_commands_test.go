package daggercmd

import (
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/require"
)

func TestReasoningEffortOptions(t *testing.T) {
	values := func(opts []huh.Option[string]) []string {
		var vs []string
		for _, o := range opts {
			vs = append(vs, o.Value)
		}
		return vs
	}

	// Catalog model marked non-reasoning: only "none", no invented levels.
	require.Equal(t, []string{"none"}, values(reasoningEffortOptions("ai21/jamba-large-1.7")))

	// Unknown model: conventional fallback.
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions("my-local-model")))

	// Catalog model that can reason but declares no explicit levels
	// (e.g. Anthropic): conventional fallback.
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions("claude-sonnet-4-5-20250929")))
}
