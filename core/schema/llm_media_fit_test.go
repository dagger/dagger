package schema

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestLLMImagesFitTheirModel(t *testing.T) {
	ctx, dag, _ := newLiveLLMMetadataTest(t, false, &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
		"anthropic": {APIKey: "env://ANTHROPIC_API_KEY"},
		"openai":    {APIKey: "env://OPENAI_API_KEY"},
	}}, &llmAuthSecrets{})

	pngOf := func(width, height int) dagql.Bytes {
		var buf bytes.Buffer
		require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height))))
		return buf.Bytes()
	}
	content := func(data dagql.Bytes) dagql.Input {
		input, err := (dagql.ArrayInput[dagql.InputObject[core.LLMContentBlockInput]]{}).Decoder().DecodeInput([]any{
			map[string]any{"kind": "TEXT", "text": "look"},
			map[string]any{"kind": "IMAGE", "mimeType": "image/png", "data": data},
		})
		require.NoError(t, err)
		return input
	}
	conversation := func(model string, sels ...dagql.Selector) *core.LLM {
		var llm dagql.ObjectResult[*core.LLM]
		require.NoError(t, dag.Select(ctx, dag.Root(), &llm, append([]dagql.Selector{
			{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.NewString(model))}}},
		}, sels...)...))
		return llm.Self()
	}
	withContent := func(data dagql.Bytes) dagql.Selector {
		return dagql.Selector{Field: "withContent", Args: []dagql.NamedInput{{Name: "content", Value: content(data)}}}
	}
	requireImage := func(llm *core.LLM, width, height int) {
		t.Helper()
		var images []*core.LLMContentBlock
		for _, msg := range llm.Messages {
			for _, block := range msg.Content {
				if block.Kind == core.LLMContentImage {
					images = append(images, block)
				}
			}
		}
		require.Len(t, images, 1)
		cfg, err := png.DecodeConfig(bytes.NewReader(images[0].Data))
		require.NoError(t, err)
		require.Equal(t, [2]int{width, height}, [2]int{cfg.Width, cfg.Height})
	}

	large := pngOf(3200, 1600)
	requireImage(conversation("claude-opus-4-6", withContent(large)), 1568, 784)
	requireImage(conversation("claude-opus-4-7", withContent(large)), 2576, 1288)

	// A GPT-5.5 conversation keeps a 5000px image, until it switches to Claude.
	huge := pngOf(5000, 2500)
	requireImage(conversation("gpt-5.5", withContent(huge)), 5000, 2500)
	requireImage(conversation("gpt-5.5", withContent(huge),
		dagql.Selector{Field: "withModel", Args: []dagql.NamedInput{{Name: "model", Value: dagql.NewString("claude-opus-4-6")}}},
	), 1568, 784)
}
