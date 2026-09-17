package schema

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/stretchr/testify/require"
)

// The protocol half of the fixture controls, in process: argument rules,
// strict contained request records, the closed barrier actions as the field
// reports them, hold tokens and retained-root removal through the field, and
// the refusal of server-only controls on a server that has none. Bytes, GC,
// offers and renewal are native (core/integration TestFixtureControls).
func TestFixtureControls(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{SessionID: "fixture", ClientID: "client"})
	cache, err := dagql.NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	facade := &fixtureTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{}, fn: &core.FunctionCall{Name: "report", ParentName: "Probe"}}
	q := core.NewRoot(facade)
	ctx = core.ContextWithQuery(ctx, q)
	srv, err := dagql.NewServer(ctx, q)
	require.NoError(t, err)
	facade.dag = srv
	root := t.TempDir()
	t.Setenv(remoteCacheFixtureGate, root)
	require.NoError(t, installRemoteCacheFixture(srv))

	control := func(name string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "control"), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "control", name), raw, 0600))
	}
	run := func(operation, path string, ids []string, out any) error {
		t.Helper()
		variableIDs := make([]any, len(ids))
		for i, id := range ids {
			variableIDs[i] = id
		}
		vars := map[string]any{"op": operation, "path": path, "ids": variableIDs}
		result, err := srv.Query(ctx, `query($op:String!,$path:String!,$ids:[ID!]!){_remoteCacheFixture(operation:$op,path:$path,ids:$ids)}`, vars)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(result["_remoteCacheFixture"])
		require.NoError(t, err)
		var text string
		require.NoError(t, json.Unmarshal(raw, &text))
		if out != nil {
			require.NoError(t, json.Unmarshal([]byte(text), out))
		}
		return nil
	}

	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Address]{}))
	address := &core.Address{Value: "held"}
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "heldAddress", Type: dagql.NewResultCallType(address.Type())}
	attached, err := cache.GetOrInitCall(ctx, "fixture", srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(address, srv, frame)
	})
	require.NoError(t, err)
	rowID, err := cache.PersistedResultID(attached)
	require.NoError(t, err)
	handle, err := call.NewEngineResultID(rowID, call.NewType(address.Type())).Encode()
	require.NoError(t, err)

	t.Run("arguments irrelevant to an operation are refused", func(t *testing.T) {
		require.ErrorContains(t, run("gc", "anything.json", nil, nil), "does not accept a path")
		require.ErrorContains(t, run("gc", "", []string{handle}, nil), "does not accept IDs")
		require.ErrorContains(t, run("hold", "", nil, nil), "requires handles")
		require.ErrorContains(t, run("barrierArm", "", nil, nil), "invalid fixture bundle path")
		require.ErrorContains(t, run("releaseHold", "token.json", []string{handle}, nil), "does not accept IDs")
		require.ErrorContains(t, run("reticulate", "", nil, nil), "unknown fixture operation")
	})

	t.Run("request records are contained and strictly decoded", func(t *testing.T) {
		for _, path := range []string{"/abs.json", "../escape.json", "a/../b.json", "./here.json", "a//b.json"} {
			require.Error(t, run("barrierArm", path, nil, nil), path)
		}
		require.Error(t, run("barrierArm", "missing.json", nil, nil))
		control("unknown-field.json", map[string]any{"key": "k", "point": "beforeFinish", "action": "pause", "extra": true})
		require.ErrorContains(t, run("barrierArm", "unknown-field.json", nil, nil), "unknown field")
		require.NoError(t, os.WriteFile(filepath.Join(root, "control", "trailing.json"), []byte(`{"key":"k","point":"beforeFinish","action":"pause"} {}`), 0600))
		require.ErrorContains(t, run("barrierArm", "trailing.json", nil, nil), "trailing content")
		outside := filepath.Join(t.TempDir(), "outside.json")
		require.NoError(t, os.WriteFile(outside, []byte(`{"key":"k","point":"beforeFinish","action":"pause"}`), 0600))
		require.NoError(t, os.Symlink(outside, filepath.Join(root, "control", "escape.json")))
		require.Error(t, run("barrierArm", "escape.json", nil, nil), "a symlink out of the fixture root is not followed")
	})

	t.Run("barrier actions are closed", func(t *testing.T) {
		control("bad-action.json", dagql.FixtureBarrierRequest{Key: "k", Point: dagql.FixtureBeforeFinish, Action: "failAnything"})
		require.ErrorContains(t, run("barrierArm", "bad-action.json", nil, nil), "unknown fixture barrier action")
		control("bad-pair.json", dagql.FixtureBarrierRequest{Key: "k", Point: dagql.FixtureBeforeFinish, Action: dagql.FixtureFailChainRead})
		require.ErrorContains(t, run("barrierArm", "bad-pair.json", nil, nil), "legal only at")
		control("arm.json", dagql.FixtureBarrierRequest{Key: "k", Point: dagql.FixtureBeforeFinish, Action: dagql.FixturePause})
		var armed dagql.FixtureBarrierArmed
		require.NoError(t, run("barrierArm", "arm.json", nil, &armed))
		require.NotZero(t, armed.Generation)
		control("stale.json", fixtureBarrierToken{Key: "k", Generation: armed.Generation + 1})
		require.ErrorContains(t, run("barrierRelease", "stale.json", nil, nil), "not armed")
		control("token.json", fixtureBarrierToken{Key: "k", Generation: armed.Generation})
		require.NoError(t, run("barrierRelease", "token.json", nil, nil))
	})

	t.Run("hold tokens and retained roots", func(t *testing.T) {
		var hold dagql.TransferFixtureHold
		require.NoError(t, run("hold", "", []string{handle}, &hold))
		require.Equal(t, []uint64{rowID}, hold.ResultIDs)
		var dropped []dagql.TransferFixtureDroppedRoot
		require.NoError(t, run("dropRetainedRoots", "", []string{handle}, &dropped))
		require.Equal(t, []dagql.TransferFixtureDroppedRoot{{ResultID: rowID, Removed: true, Registered: true}}, dropped)
		control("hold.json", fixtureHoldToken{Token: hold.Token})
		require.NoError(t, run("releaseHold", "hold.json", nil, nil))
		require.ErrorContains(t, run("releaseHold", "hold.json", nil, nil), "unknown fixture hold token")
		require.Zero(t, cache.TransferFixtureHoldCount())
	})

	t.Run("the observation bound is a positive per-scenario cap", func(t *testing.T) {
		control("cap.json", fixtureObserveRequest{Cap: 0})
		require.ErrorContains(t, run("observe", "cap.json", nil, nil), "positive cap")
		control("cap.json", fixtureObserveRequest{Cap: 64})
		require.NoError(t, run("observe", "cap.json", nil, nil))
		var report remoteCacheFixtureReport
		require.NoError(t, run("report", "", nil, &report))
		require.Zero(t, report.Controls.HoldTokens, "the hold above was released")
		require.Equal(t, 1, report.Controls.ArmedBarriers, "the barrier armed above never fired, so the report still shows it")
	})

	t.Run("transport shape", func(t *testing.T) {
		// The default transport keeps its dynamic type: the LLM dial override
		// clones it through this assertion.
		_, ok := http.DefaultTransport.(*http.Transport)
		require.True(t, ok)
		base := http.DefaultTransport
		require.Same(t, base, fixturetransport.Wrap(base), "with no dispatcher enabled the factory returns the original transport")
		control("script.json", fixturetransport.Script{})
		require.ErrorContains(t, run("transport", "script.json", nil, nil), "no fixture transport")
	})

	t.Run("server-only controls need the engine's controller", func(t *testing.T) {
		require.ErrorContains(t, run("gc", "", nil, nil), "no fixture server controls")
		control("reply.json", core.RemoteCacheFixtureRenewalReply{Chain: "sha256:0000000000000000000000000000000000000000000000000000000000000000"})
		require.ErrorContains(t, run("armRenewalReply", "reply.json", nil, nil), "no fixture server controls")
	})
}
