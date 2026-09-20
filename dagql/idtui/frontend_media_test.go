package idtui

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func frontendMediaRecords(t *testing.T, id dagui.SpanID) ([]sdklog.Record, string) {
	t.Helper()
	return frontendMediaRecordsSize(t, id, 80, 64)
}

func frontendMediaRecordsSize(t *testing.T, id dagui.SpanID, width, height int) ([]sdklog.Record, string) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))))
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	return []sdklog.Record{
		frontendTestLogRecord(id.SpanID, otellog.StringValue("before image\n"),
			otellog.String(telemetry.ContentTypeAttr, "text/markdown")),
		frontendTestLogRecord(id.SpanID, otellog.StringValue("[image: image/png]\n"),
			otellog.String(telemetryattrs.LogMediaKindAttr, "image"),
			otellog.String(telemetryattrs.LogMediaMIMETypeAttr, "image/png"),
			otellog.String(telemetryattrs.LogMediaDataAttr, data)),
		frontendTestLogRecord(id.SpanID, otellog.StringValue("after image\n"),
			otellog.String(telemetry.ContentTypeAttr, "text/markdown")),
	}, data
}

func TestPrettyMediaFallback(t *testing.T) {
	// An explicit opt-in must not affect headless terminals or text reports.
	t.Setenv("DAGGER_TUI_IMAGES", "kitty")
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.Ascii} {
		fe := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(80, 24), profile)
		require.Nil(t, fe.logs.Images)
		id := prettyTestSpanID(1)
		records, data := frontendMediaRecords(t, id)
		require.NoError(t, fe.logs.Export(context.Background(), records))
		logs := fe.logs.Logs[id]
		logs.SetWidth(80)
		logs.SetHeight(logs.UsedHeight())
		view := ansi.Strip(logs.View())
		require.Less(t, strings.Index(view, "before image"), strings.Index(view, "[image:"))
		require.Less(t, strings.Index(view, "[image:"), strings.Index(view, "after image"))
		require.NotContains(t, view, data)
		require.NotContains(t, view, "\U0010EEEE")
		var raw bytes.Buffer
		require.NoError(t, logs.PrintRaw(&raw))
		require.Equal(t, "before image\n[image: image/png]\nafter image\n", raw.String())
	}
}

func TestPrettyMediaConversationNextItem(t *testing.T) {
	db := dagui.NewDB()
	rootID, userID, nextID := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
	start := time.Unix(100, 0)
	message := dagui.SpanSnapshot{
		ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt", ParentID: rootID,
		Message: "received", LLMRole: "user", StartTime: start.Add(time.Second),
	}
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start},
		message,
	})
	db.SetPrimarySpan(rootID)
	headless := tuist.NewHeadlessTerminal(100, 30)
	images := newKittyImages()
	terminal := images.wrapTerminal(headless)
	require.NoError(t, terminal.Start(func([]byte) {}, func() {}))
	t.Cleanup(terminal.Stop)
	fe := newWithTerminalProfile(io.Discard, db, terminal, termenv.ANSI)
	fe.logs.Images = images
	fe.shell = stubShellHandler{}
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.recalculateViewLocked()
	fe.tui.Step() // establish the live terminal layout before logs arrive
	// Sixteen image rows exceed the ten-row tool/command tail window.
	records, _ := frontendMediaRecordsSize(t, userID, 80, 256)
	require.NoError(t, fe.LogExporter().Export(context.Background(), records))
	fe.recalculateViewLocked()
	require.True(t, fe.rows.BySpan[userID].Expanded)

	assertImage := func(t *testing.T) {
		t.Helper()
		frame := strings.Join(fe.tui.Step(), "\n")
		require.Equal(t, 1, strings.Count(frame, "before image"), "leading caption")
		require.Equal(t, 1, strings.Count(frame, "after image"), "trailing caption")
		require.NotContains(t, frame, "lines hidden")
		require.Equal(t, 16, strings.Count(frame, kittyMarkerPrefix), "each image row must be rendered once")
		require.Equal(t, 160, strings.Count(frame, string(kittyPlaceholder)), "ten columns in each of sixteen rows")
		for row := range 16 {
			cell := string([]rune{kittyPlaceholder, kittyDiacritics[row], kittyDiacritics[0]})
			require.Equal(t, 1, strings.Count(frame, cell), "image row %d, including the original first row", row)
		}
		require.Less(t, strings.Index(frame, "before image"), strings.Index(frame, kittyMarkerPrefix))
		require.Less(t, strings.LastIndex(frame, kittyMarkerPrefix), strings.Index(frame, "after image"))
	}
	t.Run("latest", assertImage)

	// Completing the prompt and receiving the next item auto-collapses it.
	// This used to add a second, full shell-log block below its clipped title.
	message.EndTime, message.Final = start.Add(2*time.Second), true
	db.ImportSnapshots([]dagui.SpanSnapshot{
		message,
		{ID: nextID, TraceID: prettyTestTraceID(), Name: "ReadArtifact", ParentID: rootID,
			LLMRole: "assistant", LLMTool: "ReadArtifact", StartTime: start.Add(3 * time.Second)},
	})
	fe.recalculateViewLocked()
	require.False(t, fe.rows.BySpan[userID].Expanded)
	t.Run("next item", assertImage)
	t.Run("cached repaint", assertImage)
	headless.Resize(60, 24)
	t.Run("resize", assertImage)
}

func TestPrettyMediaZoomMessage(t *testing.T) {
	for _, graphics := range []bool{false, true} {
		name := "fallback"
		if graphics {
			name = "kitty"
		}
		t.Run(name, func(t *testing.T) {
			db := dagui.NewDB()
			rootID, userID := prettyTestSpanID(1), prettyTestSpanID(2)
			start := time.Unix(100, 0)
			db.ImportSnapshots([]dagui.SpanSnapshot{
				{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start},
				{ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt", ParentID: rootID,
					Message: "received", LLMRole: "user", StartTime: start.Add(time.Second),
					EndTime: start.Add(2 * time.Second), Final: true},
			})
			db.SetPrimarySpan(rootID)
			headless := tuist.NewHeadlessTerminal(100, 30)
			images := newKittyImages()
			terminal := images.wrapTerminal(headless)
			require.NoError(t, terminal.Start(func([]byte) {}, func() {}))
			t.Cleanup(terminal.Stop)
			fe := newWithTerminalProfile(io.Discard, db, terminal, termenv.ANSI)
			if graphics {
				fe.logs.Images = images
			}
			records, _ := frontendMediaRecords(t, userID)
			require.NoError(t, fe.LogExporter().Export(context.Background(), records))
			fe.recalculateViewLocked()
			assertMedia := func(t *testing.T) {
				t.Helper()
				frame := strings.Join(fe.tui.Step(), "\n")
				require.Equal(t, 1, strings.Count(frame, "before image"))
				require.Equal(t, 1, strings.Count(frame, "after image"))
				if fe.ZoomedSpan == userID {
					require.Equal(t, 1, strings.Count(frame, "LLM prompt"), "the pinned bar titles the span, not its message")
				}
				if graphics {
					require.Equal(t, 4, strings.Count(frame, kittyMarkerPrefix))
					require.Equal(t, 40, strings.Count(frame, string(kittyPlaceholder)))
				} else {
					require.Equal(t, 1, strings.Count(frame, "[image: image/png]"))
				}
			}
			// A no-shell trace must show the message before any other view has
			// initialized the media viewport's height.
			t.Run("root", assertMedia)
			fe.ZoomedSpan = userID
			fe.recalculateViewLocked()
			t.Run("zoom", assertMedia)
			t.Run("cached repaint", assertMedia)
			headless.Resize(60, 24)
			t.Run("resize", assertMedia)
		})
	}
}

func TestPrettyMediaKittyConversation(t *testing.T) {
	for _, origin := range []string{"", "AGENT", "EVENT"} {
		t.Run(origin, func(t *testing.T) {
			db := dagui.NewDB()
			rootID, userID := prettyTestSpanID(1), prettyTestSpanID(2)
			start := time.Unix(100, 0)
			db.ImportSnapshots([]dagui.SpanSnapshot{
				{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start},
				{ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt", ParentID: rootID,
					Message: "received", LLMRole: "user", LLMOriginKind: origin, LLMOriginAgentName: "worker",
					StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Final: true},
			})
			db.SetPrimarySpan(rootID)
			// Explicit test injection exercises the real terminal writer without
			// enabling graphics in the headless production constructor.
			headless := tuist.NewHeadlessTerminal(100, 30)
			images := newKittyImages()
			terminal := images.wrapTerminal(headless)
			require.NoError(t, terminal.Start(func([]byte) {}, func() {}))
			t.Cleanup(terminal.Stop)
			fe := newWithTerminalProfile(io.Discard, db, terminal, termenv.ANSI)
			fe.logs.Images = images
			fe.shell = stubShellHandler{}
			fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
			records, data := frontendMediaRecords(t, userID)
			require.NoError(t, fe.logs.Export(context.Background(), db.IngestLogs(records)))
			fe.recalculateViewLocked()

			frame := strings.Join(fe.tui.Step(), "\n")
			require.Contains(t, frame, "before image")
			require.Contains(t, frame, "after image")
			require.Contains(t, frame, "\U0010EEEE")
			require.Contains(t, frame, "\x1b[38;2;", "role styling must preserve the image ID")
			require.NotContains(t, frame, data, "rendered frames must contain references, not image bytes")
			fe.tui.RenderOnce()
			require.Contains(t, headless.Output(), "\x1b_G", "the terminal writer must resolve image references")
			require.Contains(t, headless.Output(), "U=1", "images must use virtual placements")

			// A final report bypasses the terminal writer, including when a
			// caller requests it before Stop. It must contain safe placeholders.
			var report bytes.Buffer
			require.NoError(t, fe.FinalRender(&report))
			if origin == "EVENT" {
				// Event reports retain their existing compact one-line summary.
				require.Contains(t, report.String(), "(+2 lines)")
			} else {
				require.Contains(t, report.String(), "[image:")
			}
			require.NotContains(t, report.String(), "\U0010EEEE")
			require.NotContains(t, report.String(), "\x1b_G")
			require.NotContains(t, report.String(), data)
		})
	}
}
