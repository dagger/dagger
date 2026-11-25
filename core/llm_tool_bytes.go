package core

import (
	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/attribute"
)

// These are UTF-8 text footprints, not tokens, provider usage or billed cost.
// Media contributes neither its bytes/base64 nor the display's placeholders.
func toolResultByteAttrs(result *LLMContentBlock) []attribute.KeyValue {
	bytes := int64(len(result.Text))
	for _, block := range result.Content {
		if block.Kind == LLMContentText {
			bytes += int64(len(block.Text))
		}
	}
	scope := "text"
	if llmContentHasMedia(result.Content) {
		scope = "text_only"
	}
	return []attribute.KeyValue{
		attribute.Int64(telemetryattrs.LLMToolResultBytesAttr, bytes),
		attribute.String(telemetryattrs.LLMToolResultBytesScopeAttr, scope),
	}
}

// Provenance belongs to the individual display call. Forwarded and recorded
// argument text has a real size, but represents no additional model generation.
// The zero value deliberately makes no claim about where arguments came from.
type toolArgumentProvenance int

const (
	unknownToolArguments toolArgumentProvenance = iota
	modelToolArguments
	reserializedToolArguments
	recordedToolArguments
	forwardedToolArguments
)

func toolArgumentByteAttrs(bytes int64, provenance toolArgumentProvenance) []attribute.KeyValue {
	source, scope := "unknown", "argument_json"
	switch provenance {
	case modelToolArguments:
		source = "model"
	case reserializedToolArguments:
		source, scope = "model", "reserialized_argument_json"
	case recordedToolArguments:
		source = "recorded"
	case forwardedToolArguments:
		source = "forwarded"
	}
	return []attribute.KeyValue{
		attribute.Int64(telemetryattrs.LLMToolArgumentBytesAttr, bytes),
		attribute.String(telemetryattrs.LLMToolArgumentBytesScopeAttr, scope),
		attribute.String(telemetryattrs.LLMToolArgumentBytesSourceAttr, source),
	}
}
