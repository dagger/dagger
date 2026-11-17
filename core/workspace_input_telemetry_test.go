package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func workspaceTelemetryAttrs(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := map[string]attribute.Value{}
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func workspaceTelemetryRows(t *testing.T, attrs map[string]attribute.Value) []telemetryattrs.WorkspaceInputRecord {
	t.Helper()
	var rows []telemetryattrs.WorkspaceInputRecord
	for _, encoded := range attrs[telemetryattrs.WorkspaceInputsAttr].AsStringSlice() {
		var row telemetryattrs.WorkspaceInputRecord
		require.NoError(t, json.Unmarshal([]byte(encoded), &row))
		rows = append(rows, row)
	}
	return rows
}

func TestWorkspaceInputCallTelemetry(t *testing.T) {
	ctx, srv, _ := workspaceOperationTestServer(t)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx, parent := provider.Tracer("workspace-input-test").Start(ctx, "parent")
	defer parent.End()
	srv.Around(AroundFunc)
	bodies := 0
	dagql.Fields[*Workspace]{
		dagql.NodeFunc("directory", func(ctx context.Context, _ dagql.ObjectResult[*Workspace], args struct {
			Path             string
			Include, Exclude []string
			Fail             bool `default:"false"`
		}) (dagql.ObjectResult[*Directory], error) {
			bodies++
			if args.Fail {
				return dagql.ObjectResult[*Directory]{}, errors.New("fixture failure")
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &Directory{})
		}),
		dagql.Func("findUp", func(context.Context, *Workspace, struct{ Name, From string }) (dagql.Nullable[dagql.String], error) {
			return dagql.Null[dagql.String](), nil
		}),
		dagql.NodeFunc("__workspaceSDK", func(ctx context.Context, ws dagql.ObjectResult[*Workspace], _ struct{}) (dagql.ObjectResult[*WorkspaceSDK], error) {
			return dagql.NewObjectResultForCurrentCall(ctx, srv, &WorkspaceSDK{Workspace: ws, Name: "metadata"})
		}),
	}.Install(srv)
	var workspaces [2]dagql.ObjectResult[*Workspace]
	var boundaries [2]string
	for i := range workspaces {
		require.NoError(t, srv.Select(ctx, srv.Root(), &workspaces[i], dagql.Selector{Field: "currentWorkspace", Args: []dagql.NamedInput{{Name: "owner", Value: dagql.String(fmt.Sprint(i))}}}))
		var err error
		boundaries[i], err = workspaceResultBoundary(ctx, workspaces[i])
		require.NoError(t, err)
		workspaces[i], err = workspaces[i].WithContentDigest(ctx, digest.FromString("equal telemetry input contents"))
		require.NoError(t, err)
	}
	selection := dagql.Selector{Field: "directory", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.String("/")},
		{Name: "include", Value: dagql.ArrayInput[dagql.String]{"README.md", "docs/**"}},
		{Name: "exclude", Value: dagql.ArrayInput[dagql.String]{"docs/private/**"}},
	}}
	for _, i := range []int{0, 0, 1} {
		result, err := workspaces[i].Select(ctx, srv, selection)
		require.NoError(t, err)
		if i == 1 {
			require.True(t, result.HitCache(), "structural hit must keep the current Workspace binding")
		}
	}
	require.Equal(t, 1, bodies)
	failed := selection
	failed.Args = append(append([]dagql.NamedInput(nil), selection.Args...), dagql.NamedInput{Name: "fail", Value: dagql.Boolean(true)})
	_, err := workspaces[1].Select(ctx, srv, failed)
	require.ErrorContains(t, err, "fixture failure")
	_, err = workspaces[1].Select(ctx, srv, dagql.Selector{Field: "__workspaceSDK"})
	require.NoError(t, err)
	null, err := workspaces[1].Select(ctx, srv, dagql.Selector{Field: "findUp", Args: []dagql.NamedInput{
		{Name: "name", Value: dagql.String(".env")}, {Name: "from", Value: dagql.String("a/b")},
	}})
	require.NoError(t, err)
	require.Nil(t, null.Unwrap())
	var directories []map[string]attribute.Value
	var sdk, findUp map[string]attribute.Value
	for _, span := range recorder.Ended() {
		// Shared execution spans use the same name as the owning call span.
		if workspaceTelemetryAttrs(span.Attributes())[telemetryattrs.WcprofOpKindAttr].AsString() == "call_exec" {
			continue
		}
		if span.Name() == "Workspace.directory" {
			directories = append(directories, workspaceTelemetryAttrs(span.Attributes()))
		}
		if span.Name() == "Workspace.__workspaceSDK" {
			sdk = workspaceTelemetryAttrs(span.Attributes())
		}
		if span.Name() == "Workspace.findUp" {
			findUp = workspaceTelemetryAttrs(span.Attributes())
		}
	}
	require.Len(t, directories, 4)
	for i, attrs := range directories {
		rows := workspaceTelemetryRows(t, attrs)
		require.NotEmpty(t, rows, "attributes must be present on the ended call span")
		require.Equal(t, "read", rows[0].Role)
		require.Equal(t, "/", rows[0].Path)
		require.Equal(t, []string{"README.md", "docs/**"}, rows[0].Include)
		require.Equal(t, []string{"docs/private/**"}, rows[0].Exclude)
		owner := 0
		if i >= 2 {
			owner = 1
		}
		require.Equal(t, boundaries[owner], rows[0].Workspace)
		require.Equal(t, i != 3, attrs[telemetryattrs.WorkspaceInputsCompleteAttr].AsBool())
		require.Equal(t, i == 3, attrs[telemetryattrs.WorkspaceInputsErrorAttr].AsBool())
	}
	require.NotNil(t, findUp)
	require.True(t, findUp[telemetryattrs.WorkspaceInputsCompleteAttr].AsBool())
	require.False(t, findUp[telemetryattrs.WorkspaceInputsErrorAttr].AsBool())
	findUpRows := workspaceTelemetryRows(t, findUp)
	require.Len(t, findUpRows, 3)
	for _, row := range findUpRows {
		require.Equal(t, "read", row.Role)
		require.Equal(t, boundaries[1], row.Workspace)
		require.Equal(t, []string{".env"}, row.Include)
	}
	require.NotNil(t, sdk)
	for _, row := range workspaceTelemetryRows(t, sdk) {
		require.Equal(t, "retained", row.Role)
		require.Equal(t, boundaries[1], row.Workspace)
	}
	require.NotEmpty(t, workspaceTelemetryRows(t, sdk))
	for _, attr := range parent.(sdktrace.ReadWriteSpan).Attributes() {
		require.NotEqual(t, telemetryattrs.WorkspaceInputsAttr, string(attr.Key))
	}
}

func TestWorkspaceInputTelemetryBoundsAndFindUp(t *testing.T) {
	req := &dagql.CallRequest{ResultCall: evidenceTestFrame("telemetry")}
	o := &workspaceInputCall{request: req}
	o.telemetryEnabled.Store(true)
	ctx := context.WithValue(t.Context(), workspaceInputCallKey{}, o)
	wire := newWorkspaceInputTelemetry()
	wire.fact(workspaceInputFact{Complete: true, CapabilitiesComplete: true, FindUps: []workspaceFindUpRule{{Name: ".env", Cwd: "a/b", Workspace: "caller"}}}, nil)
	o.telemetry.Store(wire)
	attrs := workspaceTelemetryAttrs(appendWorkspaceInputAttrs(ctx, nil, req, false))
	rows := workspaceTelemetryRows(t, attrs)
	require.Len(t, rows, 3)
	for i, path := range []string{"/a/b", "/a", "/"} {
		require.Equal(t, path, rows[i].Path)
		require.Equal(t, []string{".env"}, rows[i].Include)
		require.Equal(t, []string{".env/*"}, rows[i].Exclude)
	}
	wire = newWorkspaceInputTelemetry()
	for i := 0; i < 100; i++ {
		wire.record(telemetryattrs.WorkspaceInputRecord{Role: "read", Workspace: "caller", Path: fmt.Sprint("/", i), Kind: provenance.WorkspaceRead})
	}
	wire.record(telemetryattrs.WorkspaceInputRecord{Role: "input", Workspace: "caller", Path: strings.Repeat("x", workspaceTelemetryBytes+1)})
	o.telemetry.Store(wire)
	attrs = workspaceTelemetryAttrs(appendWorkspaceInputAttrs(ctx, nil, req, false))
	require.Len(t, workspaceTelemetryRows(t, attrs), workspaceTelemetryRecords)
	require.True(t, attrs[telemetryattrs.WorkspaceInputsCompleteAttr].AsBool(), "bounded display is not incomplete capture")
	require.True(t, attrs[telemetryattrs.WorkspaceInputsTruncatedAttr].AsBool())
	require.Equal(t, int64(101-workspaceTelemetryRecords), attrs[telemetryattrs.WorkspaceInputsOmittedAttr].AsInt64())
	require.Empty(t, appendWorkspaceInputAttrs(ctx, nil, &dagql.CallRequest{ResultCall: req.ResultCall}, false), "an enclosing observer must not annotate a nested call")
	wire = newWorkspaceInputTelemetry()
	wire.fact(workspaceInputFact{Complete: true, CapabilitiesComplete: true, FindUps: []workspaceFindUpRule{{Name: "../bad", Workspace: "caller"}}}, nil)
	o.telemetry.Store(wire)
	attrs = workspaceTelemetryAttrs(appendWorkspaceInputAttrs(ctx, nil, req, false))
	require.False(t, attrs[telemetryattrs.WorkspaceInputsCompleteAttr].AsBool())
	require.NotEmpty(t, attrs[telemetryattrs.WorkspaceInputsIssuesAttr].AsStringSlice())
}
