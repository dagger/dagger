package wcprof

import (
	"bytes"
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// An op's byte count is recorded and survives a dump.
func TestOpBytesRoundtrip(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx := context.Background()

	_, op := BeginOp(ctx, OpKindRemoteCache, "remotecache.install", OpOpts{Ident: "/rootfs", Bytes: 5})
	op.SetBytes(1234)
	op.End(OutcomeOK)
	RecordOp(ctx, OpKindIO, "snapshot.import.fetch[download]", OpOpts{Bytes: 99}, 1, 2, OutcomeOK)

	var buf bytes.Buffer
	if err := r.WriteDump(&buf, false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"b":1234`)) {
		t.Fatal(`dump has no "b" key for the op's bytes`)
	}
	header, events, err := ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DumpEvent{}
	for _, ev := range events {
		got[header.Strings[ev.ClassID]] = ev
	}
	if ev := got["remotecache.install"]; ev.Bytes != 1234 || ev.OpKind != "remote_cache" {
		t.Fatalf("install op: %+v", ev)
	}
	if ev := got["snapshot.import.fetch[download]"]; ev.Bytes != 99 {
		t.Fatalf("fetch op: %+v", ev)
	}
}

// RecordOpAt and RecordWaitAt place intervals stamped with time.Time on the
// recorder's clock, under the current op.
func TestRecordAtUsesRecorderClock(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx, parent := BeginOp(context.Background(), OpKindRemoteCache, "remotecache.export", OpOpts{})

	start := time.Now()
	time.Sleep(2 * time.Millisecond)
	end := time.Now()
	id := RecordOpAt(ctx, OpKindRemoteCache, "remotecache.export.walk", OpOpts{Bytes: 7}, start, end, OutcomeOK)
	if id == 0 {
		t.Fatal("RecordOpAt recorded nothing")
	}
	RecordWaitAt(ctx, "dagql.Cache.egraphMu", WaitReasonLock, start, end)
	_, stamped := BeginOp(ctx, OpKindRemoteCache, "remotecache.install", OpOpts{Start: start})
	stamped.EndAt(OutcomeOK, 9, end)
	if RecordOpAt(ctx, OpKindRemoteCache, "zero", OpOpts{}, time.Time{}, end, OutcomeOK) != 0 {
		t.Fatal("RecordOpAt with a zero start recorded an op")
	}
	parent.End(OutcomeOK)

	var op, wait, begun *Event
	for _, ev := range drainEvents(r) {
		switch {
		case ev.Type == EventTypeOp && ev.OpID == stamped.ID():
			begun = &ev
		case ev.Type == EventTypeOp && ev.OpID == id:
			op = &ev
		case ev.Type == EventTypeWait:
			wait = &ev
		}
	}
	if op == nil || wait == nil {
		t.Fatalf("missing events: op=%v wait=%v", op, wait)
	}
	wantStart := int64(start.Sub(r.epoch))
	wantEnd := int64(end.Sub(r.epoch))
	if op.StartNS != wantStart || op.EndNS != wantEnd || op.EndNS-op.StartNS < int64(2*time.Millisecond) {
		t.Fatalf("op interval [%d,%d], want [%d,%d]", op.StartNS, op.EndNS, wantStart, wantEnd)
	}
	if op.ParentID != parent.ID() || op.Bytes != 7 {
		t.Fatalf("op: %+v", op)
	}
	if wait.ParentID != parent.ID() || wait.Reason != WaitReasonLock || wait.StartNS != wantStart || wait.EndNS != wantEnd {
		t.Fatalf("wait: %+v", wait)
	}
	if begun == nil || begun.StartNS != wantStart || begun.EndNS != wantEnd || begun.ResultID != 9 {
		t.Fatalf("op begun and ended at stamps: %+v, want [%d,%d]", begun, wantStart, wantEnd)
	}
	if got := r.strings.snapshot()[wait.IdentID]; got != "dagql.Cache.egraphMu" {
		t.Fatalf("wait ident %q", got)
	}
}

// Facts and parts are links from an op: a fact names something that was
// true of the op, a part a result's part and its size.
func TestFactAndPartLinks(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx, op := BeginOp(context.Background(), OpKindCall, "Container.from", OpOpts{})
	Fact(ctx, 0, "hit.remote", 12)
	Part(ctx, 0, "/rootfs:layers", 12, 4096)
	op.End(OutcomeHit)

	strs := func() []string { return r.strings.snapshot() }
	var fact, part *Event
	for _, ev := range drainEvents(r) {
		if ev.Type != EventTypeLink {
			continue
		}
		switch ev.LinkKind {
		case LinkKindFact:
			fact = &ev
		case LinkKindPart:
			part = &ev
		}
	}
	if fact == nil || fact.ParentID != op.ID() || fact.ResultID != 12 || strs()[fact.IdentID] != "hit.remote" {
		t.Fatalf("fact link: %+v", fact)
	}
	if part == nil || part.ParentID != op.ID() || part.ResultID != 12 || part.Bytes != 4096 || strs()[part.IdentID] != "/rootfs:layers" {
		t.Fatalf("part link: %+v", part)
	}
	if LinkKindFact.String() != "fact" || LinkKindPart.String() != "part" || OpKindRemoteCache.String() != "remote_cache" {
		t.Fatal("wire names")
	}
}

// LockRW and RLockRW record a contended acquisition of an RWMutex as a lock
// wait, and nothing when it is free.
func TestRWLockRecordsContendedWait(t *testing.T) {
	r := withTestRecorder(t, 0)
	ctx, op := BeginOp(context.Background(), OpKindCall, "Query.x", OpOpts{})

	var mu sync.RWMutex
	LockRW(ctx, &mu, "test.rw")
	mu.Unlock()
	RLockRW(ctx, &mu, "test.rw")
	mu.RUnlock()
	if evs := drainEvents(r); len(evs) != 0 {
		t.Fatalf("uncontended locks recorded %d events", len(evs))
	}

	// A reader waits for a writer, then a writer for a reader.
	mu.Lock()
	done := make(chan struct{})
	go func() {
		RLockRW(ctx, &mu, "test.rw.r")
		mu.RUnlock()
		close(done)
	}()
	for !internedIdent(r, "test.rw.r") {
		runtime.Gosched()
	}
	mu.Unlock()
	<-done
	mu.RLock()
	done = make(chan struct{})
	go func() {
		LockRW(ctx, &mu, "test.rw.w")
		mu.Unlock()
		close(done)
	}()
	for !internedIdent(r, "test.rw.w") {
		runtime.Gosched()
	}
	mu.RUnlock()
	<-done

	var idents []string
	for _, ev := range drainEvents(r) {
		if ev.Type != EventTypeWait || ev.Reason != WaitReasonLock || ev.ParentID != op.ID() {
			t.Fatalf("unexpected event %+v", ev)
		}
		idents = append(idents, r.strings.snapshot()[ev.IdentID])
	}
	if len(idents) != 2 || idents[0] != "test.rw.r" || idents[1] != "test.rw.w" {
		t.Fatalf("waits: %v, want one on test.rw.r, then one on test.rw.w", idents)
	}
}

func internedIdent(r *Recorder, s string) bool {
	r.strings.mu.RLock()
	defer r.strings.mu.RUnlock()
	_, ok := r.strings.byValue[s]
	return ok
}

// With profiling off, the new entry points record nothing and still lock.
func TestRemoteHooksDisabled(t *testing.T) {
	prev := global.Load()
	global.Store(nil)
	t.Cleanup(func() { global.Store(prev) })
	ctx := context.Background()
	now := time.Now()
	if RecordOpAt(ctx, OpKindRemoteCache, "x", OpOpts{}, now, now, OutcomeOK) != 0 {
		t.Fatal("RecordOpAt recorded while disabled")
	}
	RecordWaitAt(ctx, "x", WaitReasonLock, now, now)
	Fact(ctx, 0, "x", 1)
	Part(ctx, 0, "x", 1, 1)
	var mu sync.RWMutex
	LockRW(ctx, &mu, "x")
	mu.Unlock()
	RLockRW(ctx, &mu, "x")
	mu.RUnlock()
	var op *Op
	op.SetBytes(1)
}
