package wcprof

import (
	"bytes"
	"context"
	"testing"
)

// TestRecordOpArgvInternsMetaID: RecordOp and BeginOp with OpOpts.Argv marshal the
// argv to the canonical scalar JSON-array string, intern it as the op's MetaID, and
// round-trip it through the dump (the seam the offline analyzer reads) — for both a
// completed op event and an open op carried in the header.
func TestRecordOpArgvInternsMetaID(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()

	// RecordOp path (the native processRun emit).
	RecordOp(ctx, OpKindExecPhase, "exec.processRun",
		OpOpts{Ident: "exec-1", WorkType: WorkTypeUser, Argv: []string{"go", "build", "./..."}},
		1, 2, OutcomeOK)

	// BeginOp path, left open to exercise the open-op MetaID dump field.
	_, openExec := BeginOp(ctx, OpKindExecPhase, "exec.processRun",
		OpOpts{Ident: "exec-2", WorkType: WorkTypeUser, Argv: []string{"git", "clone", "url"}})
	_ = openExec // intentionally left open

	var buf bytes.Buffer
	if err := r.WriteDump(&buf, false); err != nil {
		t.Fatal(err)
	}
	header, events, err := ReadDump(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, ev := range events {
		if ev.Type == "op" && header.Strings[ev.IdentID] == "exec-1" {
			found = true
			if ev.MetaID == 0 {
				t.Fatal("processRun event must carry a non-zero MetaID")
			}
			if got := header.Strings[ev.MetaID]; got != `["go","build","./..."]` {
				t.Fatalf("event MetaID string = %q, want %q", got, `["go","build","./..."]`)
			}
		}
	}
	if !found {
		t.Fatal("missing completed processRun op event")
	}

	var openFound bool
	for _, oo := range header.OpenOps {
		if header.Strings[oo.IdentID] == "exec-2" {
			openFound = true
			if got := header.Strings[oo.MetaID]; got != `["git","clone","url"]` {
				t.Fatalf("open op MetaID string = %q, want %q", got, `["git","clone","url"]`)
			}
		}
	}
	if !openFound {
		t.Fatal("missing open processRun op")
	}
}

// TestRecordOpEmptyArgvNoMetaID: an exec with no argv interns no MetaID (0), so it
// stays the aggregated blob downstream (unlabeled by any per-command class).
func TestRecordOpEmptyArgvNoMetaID(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()
	RecordOp(ctx, OpKindExecPhase, "exec.processRun", OpOpts{Ident: "exec-x"}, 1, 2, OutcomeOK)
	for _, ev := range drainEvents(r) {
		if ev.MetaID != 0 {
			t.Fatalf("empty argv must intern no MetaID, got %d", ev.MetaID)
		}
	}
}
