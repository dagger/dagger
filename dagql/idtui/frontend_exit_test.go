package idtui

import (
	"errors"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/trace"
)

func TestNormalizeFrontendExit(t *testing.T) {
	t.Parallel()

	newPrimaryDB := func(failed bool) *dagui.DB {
		db := dagui.NewDB()
		spanID := dagui.SpanID{SpanID: trace.SpanID{1}}
		db.Spans.Add(&dagui.Span{
			SpanSnapshot: dagui.SpanSnapshot{
				ID:      spanID,
				Final:   true,
				Failed_: failed,
			},
		})
		db.SetPrimarySpan(spanID)
		return db
	}

	t.Run("preserves explicit error", func(t *testing.T) {
		want := errors.New("boom")
		got := normalizeFrontendExit(want, newPrimaryDB(true))
		if !errors.Is(got, want) {
			t.Fatalf("normalizeFrontendExit should preserve explicit error, got %v", got)
		}
	})

	t.Run("returns nil when primary span succeeded", func(t *testing.T) {
		if got := normalizeFrontendExit(nil, newPrimaryDB(false)); got != nil {
			t.Fatalf("normalizeFrontendExit(nil, succeeded primary) = %v, want nil", got)
		}
	})

	t.Run("returns exit error when primary span failed", func(t *testing.T) {
		got := normalizeFrontendExit(nil, newPrimaryDB(true))

		var exitErr ExitError
		if !errors.As(got, &exitErr) {
			t.Fatalf("normalizeFrontendExit(nil, failed primary) = %T, want ExitError", got)
		}
		if exitErr.Code() != 1 {
			t.Fatalf("normalizeFrontendExit(nil, failed primary) exit code = %d, want 1", exitErr.Code())
		}
	})
}
