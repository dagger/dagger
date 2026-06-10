package wcprof

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// withTestRecorder installs a fresh global recorder for the duration of the
// test.
func withTestRecorder(t *testing.T, maxEvents int64) *Recorder {
	t.Helper()
	prev := global.Load()
	r := NewRecorder(maxEvents)
	global.Store(r)
	t.Cleanup(func() {
		global.Store(prev)
	})
	return r
}

func drainEvents(r *Recorder) []Event {
	var out []Event
	for i := range r.shards {
		sh := &r.shards[i]
		sh.mu.Lock()
		out = append(out, sh.events...)
		sh.mu.Unlock()
	}
	return out
}

func TestDisabledIsNoop(t *testing.T) {
	prev := global.Load()
	global.Store(nil)
	t.Cleanup(func() { global.Store(prev) })

	ctx := context.Background()
	opCtx, op := BeginOp(ctx, OpKindCall, "Container.withExec", OpOpts{})
	if op != nil {
		t.Fatal("expected nil op when disabled")
	}
	if opCtx != ctx {
		t.Fatal("expected unchanged ctx when disabled")
	}
	op.End(OutcomeOK) // must not panic
	w := BeginWait(ctx, 1, WaitReasonLazy)
	if w != nil {
		t.Fatal("expected nil wait when disabled")
	}
	w.End() // must not panic
	Link(ctx, LinkKindResult, 0, 0, "", 1)
}

func TestOpWaitLinkRoundtrip(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()

	parentCtx, parent := BeginOp(ctx, OpKindSessionPhase, "session.query", OpOpts{ClientID: "client1"})
	childCtx, child := BeginOp(parentCtx, OpKindCall, "Container.withExec", OpOpts{Ident: "sha256:abc"})
	if CurrentOpID(childCtx) != child.ID() {
		t.Fatalf("child ctx should carry child op ID")
	}

	w := BeginWait(childCtx, 42, WaitReasonSingleflight)
	w.End()
	Link(childCtx, LinkKindReusedResult, 0, 0, "", 7)
	child.EndWithResult(OutcomeHit, 7)
	parent.End(OutcomeOK)

	events := drainEvents(r)
	var ops, waits, links int
	var childEv, parentEv *Event
	for i := range events {
		ev := &events[i]
		switch ev.Type {
		case EventTypeOp:
			ops++
			switch ev.OpID {
			case child.ID():
				childEv = ev
			case parent.ID():
				parentEv = ev
			}
		case EventTypeWait:
			waits++
			if ev.ParentID != child.ID() || ev.TargetID != 42 || ev.Reason != WaitReasonSingleflight {
				t.Fatalf("unexpected wait event: %+v", ev)
			}
		case EventTypeLink:
			links++
			if ev.ParentID != child.ID() || ev.ResultID != 7 || ev.LinkKind != LinkKindReusedResult {
				t.Fatalf("unexpected link event: %+v", ev)
			}
		}
	}
	if ops != 2 || waits != 1 || links != 1 {
		t.Fatalf("expected 2 ops, 1 wait, 1 link; got %d, %d, %d", ops, waits, links)
	}
	if childEv == nil || parentEv == nil {
		t.Fatal("missing op events")
	}
	if childEv.ParentID != parent.ID() {
		t.Fatalf("child parent = %d, want %d", childEv.ParentID, parent.ID())
	}
	if childEv.Outcome != OutcomeHit || childEv.ResultID != 7 {
		t.Fatalf("unexpected child op: %+v", childEv)
	}
	if childEv.EndNS < childEv.StartNS {
		t.Fatalf("child end %d < start %d", childEv.EndNS, childEv.StartNS)
	}
	strs := r.strings.snapshot()
	if strs[childEv.ClassID] != "Container.withExec" {
		t.Fatalf("class = %q", strs[childEv.ClassID])
	}
	if strs[childEv.IdentID] != "sha256:abc" {
		t.Fatalf("ident = %q", strs[childEv.IdentID])
	}
	if strs[parentEv.ClientID] != "client1" {
		t.Fatalf("client = %q", strs[parentEv.ClientID])
	}
}

func TestConcurrentRecording(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()

	const goroutines = 16
	const perG = 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				opCtx, op := BeginOp(ctx, OpKindCall, "Class.field", OpOpts{Ident: "id"})
				w := BeginWait(opCtx, 1, WaitReasonLazy)
				w.End()
				op.End(OutcomeOK)
			}
		}()
	}
	wg.Wait()

	events := drainEvents(r)
	want := goroutines * perG * 2
	if len(events) != want {
		t.Fatalf("got %d events, want %d", len(events), want)
	}
	seen := make(map[uint64]bool)
	for _, ev := range events {
		if ev.Type != EventTypeOp {
			continue
		}
		if seen[ev.OpID] {
			t.Fatalf("duplicate op ID %d", ev.OpID)
		}
		seen[ev.OpID] = true
	}
}

func TestEventCapDrops(t *testing.T) {
	r := withTestRecorder(t, 10)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_, op := BeginOp(ctx, OpKindCall, "x", OpOpts{})
		op.End(OutcomeOK)
	}
	events := drainEvents(r)
	if len(events) != 10 {
		t.Fatalf("got %d events, want cap 10", len(events))
	}
	if r.dropped.Load() != 40 {
		t.Fatalf("dropped = %d, want 40", r.dropped.Load())
	}
}

func TestDumpRoundtripAndFlush(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()

	_, op1 := BeginOp(ctx, OpKindCall, "Query.container", OpOpts{Ident: "dig1", ClientID: "c1"})
	op1.End(OutcomeExecuted)
	_, op2 := BeginOp(ctx, OpKindLazy, "Container.withExec", OpOpts{})
	_ = op2 // left open intentionally

	var buf bytes.Buffer
	if err := r.WriteDump(&buf, true); err != nil {
		t.Fatal(err)
	}

	header, events, err := ReadDump(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if header.SchemaVersion != DumpSchemaVersion {
		t.Fatalf("schema version = %d", header.SchemaVersion)
	}
	if header.EventCount != 1 || len(events) != 1 {
		t.Fatalf("event count = %d / %d, want 1", header.EventCount, len(events))
	}
	ev := events[0]
	if ev.Type != "op" || ev.OpKind != "call" || ev.Outcome != "executed" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if header.Strings[ev.ClassID] != "Query.container" {
		t.Fatalf("class = %q", header.Strings[ev.ClassID])
	}
	if len(header.OpenOps) != 1 || header.OpenOps[0].OpID != op2.ID() || header.OpenOps[0].Kind != "lazy" {
		t.Fatalf("open ops = %+v", header.OpenOps)
	}

	// Flush removed buffered events; a second dump is empty but keeps the
	// string table and open ops.
	var buf2 bytes.Buffer
	if err := r.WriteDump(&buf2, true); err != nil {
		t.Fatal(err)
	}
	header2, events2, err := ReadDump(bytes.NewReader(buf2.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(events2) != 0 {
		t.Fatalf("expected empty second dump, got %d events", len(events2))
	}
	if len(header2.OpenOps) != 1 {
		t.Fatalf("expected open op to persist, got %+v", header2.OpenOps)
	}
	if header2.Strings[1] == "" && len(header2.Strings) < len(header.Strings) {
		t.Fatal("string table should be retained across flush")
	}

	// Ending the open op after a flush still records an event.
	op2.End(OutcomeOK)
	events3 := drainEvents(r)
	if len(events3) != 1 || events3[0].OpID != op2.ID() {
		t.Fatalf("expected op2 end event after flush, got %+v", events3)
	}
}

func TestInternStability(t *testing.T) {
	r := withTestRecorder(t, 0)
	var wg sync.WaitGroup
	ids := make([]uint32, 64)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = r.Intern("same-string")
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("interning the same string returned different IDs")
		}
	}
	if r.Intern("") != 0 {
		t.Fatal("empty string must intern to 0")
	}
}

func TestNowMonotonic(t *testing.T) {
	r := NewRecorder(0)
	var last atomic.Int64
	for i := 0; i < 1000; i++ {
		now := r.Now()
		if now < last.Load() {
			t.Fatal("Now went backwards")
		}
		last.Store(now)
	}
}

func TestDumpDisabledRecorder(t *testing.T) {
	var r *Recorder
	err := r.WriteDump(&bytes.Buffer{}, false)
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("expected not-enabled error, got %v", err)
	}
}
