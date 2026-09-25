package agentcontrol

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func testAgent(handle string) Agent {
	return Agent{Key: Key{Namespace{"session", "trace", "incarnation"}, handle}, Revision: 1,
		Name: handle, State: "IDLE", Digest: "xxh3:0123456789abcdef", Activity: time.Unix(1, 0).UTC()}
}

type recorder struct{ record sdklog.Record }

func (r *recorder) OnEmit(_ context.Context, rec *sdklog.Record) error {
	r.record = rec.Clone()
	return nil
}
func (*recorder) ForceFlush(context.Context) error                       { return nil }
func (*recorder) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (*recorder) Shutdown(context.Context) error                         { return nil }

func sdkRecord(rec log.Record) sdklog.Record {
	out := &recorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(out))
	provider.Logger("test").Emit(context.Background(), rec)
	_ = provider.Shutdown(context.Background())
	return out.record
}

func TestOTLPRoundTrip(t *testing.T) {
	a := testAgent("chief")
	a.State, a.StopReason, a.PreTeardownState, a.Failure = "STOPPED", "SESSION", "FAILED", "original failure"
	rec := sdkRecord(a.Record())
	require.True(t, IsRecord(rec))
	got, sub, err := Decode(rec)
	require.NoError(t, err)
	require.Nil(t, sub)
	require.Equal(t, a, *got)
	state, err := got.RestoreState()
	require.NoError(t, err)
	require.Equal(t, "FAILED", state)
	for _, states := range [][]string{{"IDLE", "FAILED"}, nil} {
		s := Subscription{EdgeKey: EdgeKey{a.Namespace, "worker", "chief"}, Revision: 2, States: states}
		got, sub, err := Decode(sdkRecord(s.Record()))
		require.NoError(t, err)
		require.Nil(t, got)
		require.Equal(t, s, *sub)
	}
}

func TestRejectIncompleteControl(t *testing.T) {
	for _, mutate := range []func(*sdklog.Record){
		func(r *sdklog.Record) { r.AddAttributes(log.Int(VersionAttr, 2)) },
		func(r *sdklog.Record) { r.AddAttributes(log.String(RevisionAttr, "1")) },
		func(r *sdklog.Record) { r.AddAttributes(log.String(SessionAttr, "")) },
		func(r *sdklog.Record) { r.AddAttributes(log.String(CaptureErrorAttr, "failed")) },
		func(r *sdklog.Record) { r.AddAttributes(log.String(KindAttr, "other")) },
	} {
		r := sdkRecord(testAgent("a").Record())
		mutate(&r)
		require.True(t, IsRecord(r))
		_, _, err := Decode(r)
		require.Error(t, err)
	}
	var empty sdklog.Record
	require.False(t, IsRecord(empty))
	_, _, err := Decode(empty)
	require.Error(t, err)
}

func TestRevisionAndNamespaceIsolation(t *testing.T) {
	var idx Index
	a := testAgent("a")
	a.Revision = 20
	changed, err := idx.ApplyAgent(a)
	require.NoError(t, err)
	require.True(t, changed)
	old := a
	old.Revision, old.State = 1, "RUNNING"
	changed, err = idx.ApplyAgent(old)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, []Agent{a}, idx.Agents())
	other := old
	other.Session, other.Trace, other.Incarnation = "restored-session", "new-trace", "new-incarnation"
	changed, err = idx.ApplyAgent(other)
	require.NoError(t, err)
	require.True(t, changed, "a new incarnation's revision starts independently")
	require.Len(t, idx.Agents(), 2)
	_, err = idx.ApplyAgent(a)
	require.NoError(t, err, "duplicate delivery is idempotent")
	a.State = "PAUSED"
	_, err = idx.ApplyAgent(a)
	require.ErrorContains(t, err, "conflicting")
}

func TestSubscriptionRemovalAndReplacement(t *testing.T) {
	var idx Index
	s := Subscription{EdgeKey: EdgeKey{testAgent("a").Namespace, "worker", "chief"}, Revision: 1, States: []string{"IDLE", "FAILED"}}
	_, err := idx.ApplySubscription(s)
	require.NoError(t, err)
	s.States[0] = "PAUSED"
	require.Equal(t, []string{"FAILED", "IDLE"}, idx.Subscriptions()[0].States, "index owns its state array")
	s.Revision = 2
	_, err = idx.ApplySubscription(s)
	require.NoError(t, err)
	s.Revision, s.States = 3, nil
	_, err = idx.ApplySubscription(s)
	require.NoError(t, err)
	s.Revision, s.States = 2, []string{"IDLE"}
	changed, err := idx.ApplySubscription(s)
	require.NoError(t, err)
	require.False(t, changed)
	require.Empty(t, idx.Subscriptions()[0].States, "older imports cannot resurrect a removed edge")
}

func TestFinalRosterWitness(t *testing.T) {
	a, b := testAgent("chief"), testAgent("worker")
	b.Parent = a.Handle
	s := Subscription{EdgeKey: EdgeKey{a.Namespace, b.Handle, a.Handle}, Revision: 1, States: []string{"IDLE"}}
	want := Expectation{Agents: map[Key]int64{a.Key: 1, b.Key: 1}, Subscriptions: map[EdgeKey]int64{s.EdgeKey: 1}}
	var idx Index
	_, err := idx.ApplyAgent(a)
	require.NoError(t, err)
	require.Error(t, idx.Verify(want), "a completely missing agent is detected")
	_, err = idx.ApplyAgent(b)
	require.NoError(t, err)
	require.Error(t, idx.Verify(want), "a completely missing edge is detected")
	_, err = idx.ApplySubscription(s)
	require.NoError(t, err)
	require.NoError(t, idx.Verify(want))
	want.Agents[b.Key] = 2
	require.Error(t, idx.Verify(want), "received high-water marks do not prove finality")
	b.Revision, b.Digest, b.CaptureError = 2, "", "capture failed"
	_, err = idx.ApplyAgent(b)
	require.NoError(t, err)
	require.ErrorContains(t, idx.Verify(want), "capture failed", "an older digest cannot satisfy a new failed capture")
}

func TestParentCyclesAndRemovedRevisions(t *testing.T) {
	a, b := testAgent("a"), testAgent("b")
	a.Parent, b.Parent = "b", "a"
	want := Expectation{Agents: map[Key]int64{a.Key: 1, b.Key: 1}}
	var idx Index
	_, err := idx.ApplyAgent(a)
	require.NoError(t, err)
	_, err = idx.ApplyAgent(b)
	require.NoError(t, err)
	require.ErrorContains(t, idx.Verify(want), "parent cycle")
	b.Revision, b.Parent, b.Removed = 2, "", true
	_, err = idx.ApplyAgent(b)
	require.NoError(t, err)
	want.Agents[b.Key] = 2
	require.ErrorContains(t, idx.Verify(want), "outside restore roster")
	a.Revision, a.Parent = 2, ""
	_, err = idx.ApplyAgent(a)
	require.NoError(t, err)
	want.Agents[a.Key] = 2
	require.NoError(t, idx.Verify(want), "removed identities remain witnessed but are not executable")
	a.Revision, a.Parent = 3, "b"
	a.Session = "other"
	_, err = idx.ApplyAgent(a)
	require.NoError(t, err)
	want.Agents[a.Key] = 3
	require.ErrorContains(t, idx.Verify(want), "outside restore roster", "parents never resolve across namespaces")
}

func TestEmptySubscriptionCanonicalization(t *testing.T) {
	var idx Index
	s := Subscription{EdgeKey: EdgeKey{testAgent("a").Namespace, "a", "b"}, Revision: 1, States: []string{}}
	_, err := idx.ApplySubscription(s)
	require.NoError(t, err)
	_, decoded, err := Decode(sdkRecord(s.Record()))
	require.NoError(t, err)
	changed, err := idx.ApplySubscription(*decoded)
	require.NoError(t, err)
	require.False(t, changed)
}

func TestRestoreStateRequiresExplicitFacts(t *testing.T) {
	for _, state := range []string{"", "UNKNOWN"} {
		a := testAgent("a")
		a.State = state
		_, err := a.RestoreState()
		require.Error(t, err)
	}
	for _, state := range []string{"RUNNING", "WAITING_INPUT", "IDLE", "PAUSED", "FAILED", "STOPPED"} {
		a := testAgent("a")
		a.State, a.StopReason, a.PreTeardownState, a.Failure = "STOPPED", "SESSION", state, "failure"
		got, err := a.RestoreState()
		require.NoError(t, err)
		if state == "RUNNING" || state == "WAITING_INPUT" {
			state = "IDLE"
		}
		require.Equal(t, state, got)
	}
}
