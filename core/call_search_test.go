package core

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/clientdb"
)

// findCallsFullLoad is FindCalls as it was before it streamed the frames:
// every frame of every store loaded into one DB, then grepped. It is the
// reference findCallsIn must render identically to.
func findCallsFullLoad(ctx context.Context, read *clientdb.DB, re *regexp.Regexp, limit int, exclude string) (string, error) {
	db := dagui.NewDB()
	stores, release, err := read.InspectionStores(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	for _, source := range stores {
		frames, err := source.SelectCallFrames(ctx, source.CallDigests())
		if err != nil {
			return "", fmt.Errorf("select call frames: %w", err)
		}
		for start := 0; start < len(frames); start += traceReportBatchSize {
			if err := ingestCallFrames(ctx, db, frames[start:min(start+traceReportBatchSize, len(frames))]); err != nil {
				return "", err
			}
		}
	}
	if len(db.Calls) == 0 {
		return "(no calls in this session)", nil
	}
	total := len(db.Calls)
	hidden := 0
	if exclude != "" {
		for dig := range agentConversationCalls(db.Calls, exclude) {
			if re.MatchString(dagui.CallSearchText(db.Calls[dig])) {
				hidden++
			}
			delete(db.Calls, dig)
		}
	}
	var note string
	if hidden > 0 {
		note = fmt.Sprintf("(%d matching calls of your own conversation hidden; includeSelf: true lists them)", hidden)
	}
	lines := db.GrepCalls(re, limit)
	if len(lines) == 0 {
		empty := fmt.Sprintf("(no calls matching /%s/ among the %d in this session)", re, total)
		if note != "" {
			empty += "\n" + note
		}
		return empty, nil
	}
	if note != "" {
		lines = append(lines, note)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// TestFindCallsMatchesFullLoad runs call searches through the streaming
// search and the full load, over sessions exercising both transports (span
// and payload log), conversations with ownership stamps, imported traces
// holding copies of live frames and frames of their own, more frames than a
// chunk, and the line and byte caps, and requires identical output.
func TestFindCallsMatchesFullLoad(t *testing.T) {
	ctx := t.Context()
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.DiscardHandler))

	recipe, _, _ := callInspectStore(t, callInspectRecipe(t))
	conversation, _, _ := callInspectStore(t, conversationRecipe(t))
	session := callSessionStore(t, 3*findCallsChunk+100)
	empty, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "empty")
	require.NoError(t, err)
	t.Cleanup(func() { empty.Close() })

	// A live session with imports: one holding copies of live frames under
	// other names (live wins), one with frames only it has.
	live := callSessionStore(t, findCallsChunk+50)
	frames := callSessionFrames(findCallsChunk + 50)
	for i, frame := range frames {
		if i%3 == 0 {
			frame.Field = "impostor"
		}
	}
	imported, err := live.ImportTrace(ctx, "11111111111111111111111111111111", func(dst *clientdb.DB) error {
		_, err := dst.AppendLogs(callSessionLogs(t, frames))
		return err
	})
	require.NoError(t, err)
	require.NoError(t, imported.Close())
	extra := callSessionFrames(200)
	for _, frame := range extra {
		frame.Digest = strings.Replace(frame.Digest, "xxh3:0", "xxh3:f", 1)
		if frame.ReceiverDigest != "" {
			frame.ReceiverDigest = strings.Replace(frame.ReceiverDigest, "xxh3:0", "xxh3:f", 1)
		}
	}
	imported, err = live.ImportTrace(ctx, "22222222222222222222222222222222", func(dst *clientdb.DB) error {
		_, err := dst.AppendLogs(callSessionLogs(t, extra))
		return err
	})
	require.NoError(t, err)
	require.NoError(t, imported.Close())

	stores := map[string]*clientdb.DB{
		"recipe": recipe, "conversation": conversation, "session": session, "empty": empty, "imports": live,
	}
	patterns := []string{".", "needle", "thought", "withExec", "impostor", "xxh3:f", `recv=xxh3:0+1\b`, "SIGBLOB", "nothing at all", "LLM$"}
	compared := 0
	for name, store := range stores {
		for _, pattern := range patterns {
			re := regexp.MustCompile(pattern)
			for _, limit := range []int{0, 1, 3, 200} {
				for _, exclude := range []string{"", "me", "worker", "nobody"} {
					want, wantErr := findCallsFullLoad(ctx, store, re, limit, exclude)
					got, gotErr := findCallsIn(ctx, store, re, limit, exclude)
					require.Equal(t, wantErr, gotErr)
					require.Equal(t, want, got, "%s /%s/ limit %d exclude %q", name, pattern, limit, exclude)
					compared++
				}
			}
		}
	}
	t.Logf("compared %d searches", compared)
}
