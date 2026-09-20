package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type mediaLogRecorder struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (r *mediaLogRecorder) OnEmit(_ context.Context, rec *sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec.Clone())
	return nil
}
func (*mediaLogRecorder) Shutdown(context.Context) error                         { return nil }
func (*mediaLogRecorder) ForceFlush(context.Context) error                       { return nil }
func (*mediaLogRecorder) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

func TestMediaTelemetryOrdering(t *testing.T) {
	blocks := []*LLMContentBlock{
		{Kind: LLMContentText, Text: "before"},
		{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
		{Kind: LLMContentText, Text: "between"},
		{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "YXVkaW8="},
		{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "ZG9j"},
		{Kind: LLMContentText, Text: "after"},
	}
	for _, mode := range []string{"user", "live tool", "history tool"} {
		t.Run(mode, func(t *testing.T) {
			_, ctx := recordingTestRecorder(t)
			recorder := &mediaLogRecorder{}
			provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder))
			ctx = telemetry.WithLoggerProvider(ctx, provider)
			result := &LLMContentBlock{Kind: LLMContentToolResult, CallID: "call", Text: "prefix", Content: blocks}
			switch mode {
			case "user":
				emitUserMessageSpan(ctx, &LLMMessage{Role: LLMMessageRoleUser, Content: blocks}, "")
			case "live tool":
				got := newMCP().CallContent(ctx, []LLMTool{{Name: "media", Call: func(context.Context, any) (any, error) {
					return result, nil
				}}}, &LLMToolCall{Name: "media", CallID: "call"})
				require.False(t, got.Errored)
			case "history tool":
				llm := &LLM{Messages: []*LLMMessage{
					{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call", ToolName: "media", Arguments: JSON("{}")}}},
					{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{result}},
				}}
				llm.EmitHistory(ctx)
			}

			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			var bodies strings.Builder
			var ordered []string
			mediaCount := 0
			for _, record := range recorder.records {
				body, ok := dagui.LogBodyString(record)
				require.True(t, ok)
				if body == "" || body == "{}\n" { // EOF and replayed arguments
					continue
				}
				bodies.WriteString(body)
				for _, block := range blocks {
					if block.Data != "" {
						require.NotContains(t, body, block.Data)
					}
				}
				if media, ok := dagui.ParseMediaRecord(record); ok {
					ordered = append(ordered, media.Kind)
					want := blocks[[]int{1, 3, 4}[mediaCount]]
					require.Equal(t, want.Data, media.Data)
					require.Equal(t, want.MIMEType, media.MIMEType)
					require.True(t, record.SpanID().IsValid())
					if mode != "user" {
						verbose := false
						record.WalkAttributes(func(kv otellog.KeyValue) bool {
							if kv.Key == telemetry.LogsVerboseAttr {
								verbose = kv.Value.AsBool()
							}
							return true
						})
						require.True(t, verbose)
					}
					mediaCount++
				} else {
					ordered = append(ordered, strings.TrimSuffix(body, "\n"))
				}
			}
			wantOrder := []string{"before", "image", "between", "audio", "document", "after"}
			if mode == "user" {
				require.Equal(t, "before[image: image/png]\nbetween[audio: audio/wav]\n[document: application/pdf]\nafter", bodies.String())
			} else {
				wantOrder = append([]string{"prefix"}, wantOrder...)
				require.Equal(t, result.ContentText()+"\n", bodies.String())
			}
			require.Equal(t, wantOrder, ordered)
			require.Equal(t, 3, mediaCount)
		})
	}
}
