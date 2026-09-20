package core

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
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

func TestNewMessageSpansAcrossToolResults(t *testing.T) {
	image := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="}
	screenshot := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentText, Text: "Browser screenshot"}, image,
	}}
	result := &LLMContentBlock{Kind: LLMContentToolResult, CallID: "screenshot", Text: "Continuing from the returned conversation."}
	resultMsg := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{result}}
	assistant := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
		{Kind: LLMContentToolCall, CallID: "screenshot", ToolName: "viewScreenshot"},
	}}
	for _, tc := range []struct {
		name     string
		messages []*LLMMessage
		want     string
		prompts  int
		images   int
	}{
		{
			name: "continuation content before result",
			// A tool transforms the LLM with withContent; step then appends its
			// continuation notice before the next step emits pending prompts.
			messages: []*LLMMessage{assistant, screenshot, resultMsg},
			want:     "Browser screenshot[image: image/png]\n", prompts: 1, images: 1,
		},
		{
			name:     "content after result",
			messages: []*LLMMessage{assistant, resultMsg, screenshot},
			want:     "Browser screenshot[image: image/png]\n", prompts: 1, images: 1,
		},
		{
			name: "interleaved results preserve prompt order",
			messages: []*LLMMessage{assistant, resultMsg, screenshot, resultMsg,
				{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "follow-up"}}}, resultMsg},
			want: "Browser screenshot[image: image/png]\nfollow-up", prompts: 2, images: 1,
		},
		{
			name: "mixed message keeps direct media but not result output",
			messages: []*LLMMessage{assistant, {Role: LLMMessageRoleUser,
				Content: []*LLMContentBlock{result, image}}, resultMsg},
			want: "[image: image/png]\n", prompts: 1, images: 1,
		},
		{
			name:     "previous assistant remains the boundary",
			messages: []*LLMMessage{screenshot, assistant, resultMsg},
		},
		{
			name: "nested tool media is already emitted by the call",
			messages: []*LLMMessage{assistant, {Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
				{Kind: LLMContentToolResult, CallID: "screenshot", Content: []*LLMContentBlock{image}},
			}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans, ctx := recordingTestRecorder(t)
			recorder := &mediaLogRecorder{}
			provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder))
			ctx = telemetry.WithLoggerProvider(ctx, provider)
			emitNewMessageSpans(ctx, tc.messages, "xxh3:continuation")

			var body strings.Builder
			images := 0
			for _, record := range recorder.records {
				if text, ok := dagui.LogBodyString(record); ok {
					body.WriteString(text)
				}
				if media, ok := dagui.ParseMediaRecord(record); ok {
					require.Equal(t, "image", media.Kind)
					require.Equal(t, image.Data, media.Data)
					images++
				}
			}
			require.Equal(t, tc.want, body.String())
			require.NotContains(t, body.String(), image.Data)
			require.Equal(t, tc.images, images)
			require.Len(t, spans.Ended(), tc.prompts, "tool-result-only messages must not create empty prompt spans")
			for _, span := range spans.Ended() {
				require.Equal(t, "LLM prompt", span.Name())
				digest, ok := spanAttr(span, telemetryattrs.LLMCallDigestAttr)
				require.True(t, ok)
				require.Equal(t, "xxh3:continuation", digest.AsString())
			}
		})
	}
}
