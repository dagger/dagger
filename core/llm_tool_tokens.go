package core

import (
	"context"
	"strings"
	"sync"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/tiktoken-go/tokenizer/codec"
	"go.opentelemetry.io/otel/attribute"
)

// Tool result counts describe the returned text, not chat framing, media or
// billed usage. Keep the model that generated the call: a continuation may
// change the conversation's endpoint before its result is finalized.
type toolResultTokenModel struct {
	provider LLMProvider
	model    string
}

type toolResultTokenModelKey struct{}

func withToolResultTokenModel(ctx context.Context, endpoint *LLMEndpoint) context.Context {
	return context.WithValue(ctx, toolResultTokenModelKey{}, toolResultTokenModel{
		provider: endpoint.Provider,
		model:    endpoint.Model,
	})
}

// Only trusted provider/model pairs use a tokenizer. An OpenAI-compatible API
// does not imply an OpenAI tokenizer. This deliberately small allowlist follows
// openai/tiktoken's model.py; unknown names (including deployment aliases) fall
// back rather than guessing from arbitrary model-name prefixes.
func (m toolResultTokenModel) encoding() string {
	if m.provider != OpenAI && m.provider != OpenAICodex {
		return ""
	}
	// Accept dated snapshots of the listed models, not arbitrary suffixes.
	model := m.model
	if n := len(model); n > 11 && model[n-11] == '-' {
		date := model[n-10:]
		if date[4] == '-' && date[7] == '-' && strings.Count(date, "-") == 2 {
			digits := strings.ReplaceAll(date, "-", "")
			if strings.Trim(digits, "0123456789") == "" {
				model = model[:n-11]
			}
		}
	}
	switch model {
	case "gpt-4", "gpt-4-turbo", "gpt-3.5-turbo":
		return "cl100k_base"
	case "gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano",
		"o1", "o1-mini", "o1-preview", "o3", "o3-mini", "o4-mini",
		"gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-5-codex",
		"gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex-max",
		"gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.4":
		return "o200k_base"
	default:
		return ""
	}
}

// The vocabulary is embedded in Go data. Initialize only the two encodings we
// use, once, and serialize access to each reusable codec. Counting never fetches
// a vocabulary, resolves an endpoint, or calls a provider API.
type toolResultCodec struct {
	mu    sync.Mutex
	codec *codec.Codec
}

var (
	toolResultCl100k = sync.OnceValue(func() *toolResultCodec {
		c := codec.NewCl100kBase()
		c.SetErrorMode(codec.ErrorModeStrict)
		return &toolResultCodec{codec: c}
	})
	toolResultO200k = sync.OnceValue(func() *toolResultCodec {
		c := codec.NewO200kBase()
		c.SetErrorMode(codec.ErrorModeStrict)
		return &toolResultCodec{codec: c}
	})
)

func toolResultTokenAttrs(ctx context.Context, result *LLMContentBlock) []attribute.KeyValue {
	text := result.ContentText()
	model, _ := ctx.Value(toolResultTokenModelKey{}).(toolResultTokenModel)
	count := estimateTextTokens(len(text))
	method, encoding, scope := "bytes_div_4", "", "text"
	if llmContentHasMedia(result.Content) {
		// ContentText substitutes placeholders for media. Do not present their
		// tokenized length as the size of the model's multimodal input.
		scope = "text_projection"
	} else if name := model.encoding(); name != "" {
		var counter *toolResultCodec
		if name == "cl100k_base" {
			counter = toolResultCl100k()
		} else {
			counter = toolResultO200k()
		}
		counter.mu.Lock()
		// Count treats special-token-looking strings as ordinary text. Strict
		// UTF-8 makes malformed tool output take the labeled fallback instead.
		n, err := counter.codec.Count(text)
		counter.mu.Unlock()
		if err == nil {
			count, method, encoding = int64(n), "tokenizer", name
		}
	}
	attrs := []attribute.KeyValue{
		attribute.Int64(telemetryattrs.LLMToolResultTokensAttr, count),
		attribute.String(telemetryattrs.LLMToolResultTokensMethodAttr, method),
		attribute.String(telemetryattrs.LLMToolResultTokensScopeAttr, scope),
	}
	if encoding != "" {
		attrs = append(attrs, attribute.String(telemetryattrs.LLMToolResultTokensEncodingAttr, encoding))
	}
	if model.model != "" {
		attrs = append(attrs, attribute.String(telemetryattrs.LLMToolResultTokensModelAttr, model.model))
	}
	return attrs
}
