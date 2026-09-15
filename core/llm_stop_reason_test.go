package core

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

// A turn cut short has to surface as a ModelFinishedError even when partial
// content streamed before it stopped, so each provider's reason check is
// exercised on its own rather than through a live client.

func TestAnthropicStoppedCleanly(t *testing.T) {
	for _, reason := range []anthropic.StopReason{
		anthropic.StopReasonEndTurn,
		anthropic.StopReasonToolUse,
		anthropic.StopReasonStopSequence,
		anthropic.StopReasonPauseTurn,
		"",                        // not reported
		"some_reason_added_later", // unknown reasons stay usable
	} {
		assert.True(t, anthropicStoppedCleanly(reason), "reason %q should be clean", reason)
	}

	for _, reason := range []anthropic.StopReason{
		anthropic.StopReasonRefusal,
		anthropic.StopReasonMaxTokens,
		"model_context_window_exceeded",
	} {
		assert.False(t, anthropicStoppedCleanly(reason), "reason %q should end the turn", reason)
	}
}

func TestOpenAIFinishedCleanly(t *testing.T) {
	for _, reason := range []string{"stop", "tool_calls", "function_call", ""} {
		assert.True(t, openAIFinishedCleanly(reason), "reason %q should be clean", reason)
	}

	for _, reason := range []string{"length", "content_filter"} {
		assert.False(t, openAIFinishedCleanly(reason), "reason %q should end the turn", reason)
	}
}

func TestGenaiFinishedCleanly(t *testing.T) {
	// Empty is the common case: Gemini reports a finish reason only on the last
	// streamed chunk.
	for _, reason := range []genai.FinishReason{
		"",
		genai.FinishReasonStop,
		genai.FinishReasonUnspecified,
	} {
		assert.True(t, genaiFinishedCleanly(reason), "reason %q should be clean", reason)
	}

	for _, reason := range []genai.FinishReason{
		genai.FinishReasonSafety,
		genai.FinishReasonMaxTokens,
		genai.FinishReasonRecitation,
		genai.FinishReasonBlocklist,
		genai.FinishReasonProhibitedContent,
		genai.FinishReasonMalformedFunctionCall,
		genai.FinishReasonOther,
	} {
		assert.False(t, genaiFinishedCleanly(reason), "reason %q should end the turn", reason)
	}
}
