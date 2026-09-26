package daggercmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cloud/otlpstream"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/log"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

type cloudTestFrontend struct{ *restoreTestFrontend }

func (f cloudTestFrontend) EncodedIDForCallDigest(d string) (string, error) {
	id, err := f.db.CallIDForDigest(d)
	if err != nil {
		return "", err
	}
	return id.Encode()
}

type cloudRestoreFunc func(context.Context, string, cloud.TraceImportSink) error

func (f cloudRestoreFunc) FetchTrace(ctx context.Context, id string, sink cloud.TraceImportSink) error {
	return f(ctx, id, sink)
}

func cloudControlFixture(t *testing.T) (agentcontrol.Agent, agentcontrol.Agent, agentcontrol.Subscription, []log.Record) {
	t.Helper()
	_, chief, worker, edge := canonicalArchive()
	root := call.New().Append(&ast.Type{NamedType: "LLM", NonNull: true}, "llm")
	child := root.Append(&ast.Type{NamedType: "LLM", NonNull: true}, "withPrompt", call.WithArgs(call.NewArgument("prompt", call.NewLiteralString("worker"), false)))
	chief.Digest, worker.Digest = root.Digest().String(), child.Digest().String()
	pb, err := child.ToProto()
	require.NoError(t, err)
	var records []log.Record
	for _, frame := range pb.GetRecipe().CallsByDigest {
		data, err := proto.Marshal(frame)
		require.NoError(t, err)
		var rec log.Record
		rec.SetBody(log.BytesValue(data))
		rec.AddAttributes(log.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType), log.String(telemetryattrs.CallPayloadDigestAttr, frame.Digest))
		records = append(records, rec)
	}
	return chief, worker, edge, records
}

func TestCloudRestoreFallbackHTTP(t *testing.T) {
	chief, worker, edge, payloads := cloudControlFixture(t)
	old := chief
	old.Revision, old.State = chief.Revision-1, "RUNNING"
	payloads = append(payloads, chief.Record(), worker.Record(), edge.Record(), old.Record())
	data, err := proto.Marshal(controlLogs(payloads...))
	require.NoError(t, err)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		require.Empty(t, r.URL.RawQuery, "whole Cloud trace avoids archive-only selectors")
		w.Header().Set("Content-Type", otlpstream.ContentType)
		frames := otlpstream.NewFrameWriter(w)
		if strings.Contains(r.URL.Path, "/logs/") {
			require.NoError(t, frames.WriteData(data))
		}
		require.NoError(t, frames.WriteTerminal())
	}))
	defer server.Close()
	source, err := cloud.NewOTLPClient(t.Context(), &auth.Cloud{Token: &oauth2.Token{AccessToken: "token", TokenType: "Bearer"}})
	require.NoError(t, err)
	source, err = source.WithBaseURL(server.URL)
	require.NoError(t, err)
	local := &restoreTestArchive{bootstrapErr: archive.ErrCleanMiss}
	req := restoreRequest()
	req.source, req.cloudSource = local, source
	fe := cloudTestFrontend{newRestoreTestFrontend()}
	var applied bool
	fe.barrier = func(context.Context) error { applied = true; return nil }
	target := newFakeRestoreTarget()
	cleanup, err := restoreTraceSources(t.Context(), fe, target, req)
	require.NoError(t, err)
	cleanup()
	require.True(t, applied)
	require.EqualValues(t, 3, requests.Load())
	require.Equal(t, "chief", target.focused)
	require.Contains(t, target.calls, "subscribe:handle:worker:handle:chief:[FAILED IDLE]")
}

func TestCloudRestoreUnavailableLocalAPI(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "archive API unavailable", status) }))
			defer server.Close()
			local, err := archive.NewClientWithURL(server.Client(), server.URL)
			require.NoError(t, err)
			chief, worker, edge, records := cloudControlFixture(t)
			records = append(records, chief.Record(), worker.Record(), edge.Record())
			req := restoreRequest()
			req.source = local
			req.cloudSource = cloudRestoreFunc(func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
				return sink.ImportLogs(ctx, controlLogs(records...))
			})
			cleanup, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, newFakeRestoreTarget(), req)
			require.NoError(t, err)
			cleanup()
		})
	}
	// A transport failure after importing a local bootstrap is not a clean
	// source switch, and cancellation is never retried through Cloud.
	require.False(t, canFallbackToCloud(t.Context(), archive.ErrTransient))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.False(t, canFallbackToCloud(ctx, &archiveUnavailableError{archive.ErrTransient}))
	require.True(t, canFallbackToCloud(t.Context(), &archiveUnavailableError{archive.ErrTransient}))
}

func TestCloudRestoreSourceDecisions(t *testing.T) {
	for _, failure := range []error{archive.ErrCorrupt, archive.ErrState, &archive.RequestError{Kind: archive.ErrorTransient, StatusCode: http.StatusForbidden}} {
		t.Run(failure.Error(), func(t *testing.T) {
			req := restoreRequest()
			req.source = &restoreTestArchive{bootstrapErr: failure}
			req.cloudSource = cloudRestoreFunc(func(context.Context, string, cloud.TraceImportSink) error { t.Fatal("must not fall back"); return nil })
			_, err := restoreTraceSources(t.Context(), newRestoreTestFrontend(), newFakeRestoreTarget(), req)
			require.ErrorIs(t, err, failure)
		})
	}
	t.Run("local preferred", func(t *testing.T) {
		local, _, _, _ := canonicalArchive()
		req := restoreRequest()
		req.source = local
		req.cloudSource = cloudRestoreFunc(func(context.Context, string, cloud.TraceImportSink) error {
			t.Fatal("local hit must not fetch Cloud")
			return nil
		})
		cleanup, err := restoreTraceSources(t.Context(), newRestoreTestFrontend(), newFakeRestoreTarget(), req)
		require.NoError(t, err)
		cleanup()
	})
}

func TestCloudRestoreRejectsIncompleteObservedData(t *testing.T) {
	for _, kind := range []string{"missing trace", "fetch failure", "corrupt payload", "missing payload", "missing parent", "missing subscriber", "equivocation"} {
		t.Run(kind, func(t *testing.T) {
			chief, worker, edge, records := cloudControlFixture(t)
			req := restoreRequest()
			switch kind {
			case "corrupt payload":
				records[0].SetBody(log.BytesValue([]byte{0xff}))
			case "missing payload":
				records = nil
			case "missing parent":
				worker.Parent = "absent"
			case "missing subscriber":
				edge.Subscriber = "absent"
			}
			records = append(records, chief.Record(), worker.Record(), edge.Record())
			switch kind {
			case "missing trace":
				records = nil
			case "equivocation":
				chief.State = "PAUSED"
				records = append(records, chief.Record())
			}
			req.source = &restoreTestArchive{bootstrapErr: archive.ErrCleanMiss}
			req.cloudSource = cloudRestoreFunc(func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
				if err := sink.ImportLogs(ctx, controlLogs(records...)); err != nil {
					return err
				}
				if kind == "fetch failure" {
					return errors.New("truncated Cloud stream")
				}
				return sink.Seal(ctx)
			})
			target := newFakeRestoreTarget()
			_, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, target, req)
			require.Error(t, err)
			require.Empty(t, target.calls, "failure must precede runtime creation")
		})
	}
}

// TestCloudRestoreSkipsIncompleteSnapshot: recipe closures are checked per
// snapshot, so a frame only the worker's snapshot needs going missing skips
// the worker with a warning; the chief still restores.
func TestCloudRestoreSkipsIncompleteSnapshot(t *testing.T) {
	warnings := captureRestoreWarnings(t)
	chief, worker, edge, records := cloudControlFixture(t)
	records = slices.DeleteFunc(records, func(rec log.Record) bool {
		frame := new(callpbv1.Call)
		require.NoError(t, proto.Unmarshal(rec.Body().AsBytes(), frame))
		return frame.Digest == worker.Digest
	})
	records = append(records, chief.Record(), worker.Record(), edge.Record())
	req := restoreRequest()
	req.source = &restoreTestArchive{bootstrapErr: archive.ErrCleanMiss}
	req.cloudSource = cloudRestoreFunc(func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
		return sink.ImportLogs(ctx, controlLogs(records...))
	})
	target := newFakeRestoreTarget()
	cleanup, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, target, req)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, []string{"rehydrate:chief", "adopt:chief", "focus:chief"}, target.calls)
	require.Contains(t, warnings.String(), "worker (worker)")
	require.Contains(t, warnings.String(), "missing call payload")
}

// TestCloudRestoreSkipsCaptureFailure: a worker whose latest record is a
// capture failure is skipped with a warning; the chief still restores.
func TestCloudRestoreSkipsCaptureFailure(t *testing.T) {
	warnings := captureRestoreWarnings(t)
	chief, worker, edge, records := cloudControlFixture(t)
	worker.Digest, worker.CaptureError = "", "Host.directory is session-local"
	records = append(records, chief.Record(), worker.Record(), edge.Record())
	req := restoreRequest()
	req.source = &restoreTestArchive{bootstrapErr: archive.ErrCleanMiss}
	req.cloudSource = cloudRestoreFunc(func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
		return sink.ImportLogs(ctx, controlLogs(records...))
	})
	target := newFakeRestoreTarget()
	cleanup, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, target, req)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, []string{"rehydrate:chief", "adopt:chief", "focus:chief"}, target.calls)
	require.Contains(t, warnings.String(), "worker (worker)")
	require.Contains(t, warnings.String(), "Host.directory is session-local")
}

// TestUnsealedArchiveRestoresLatestRecordedState: a local archive whose engine
// stopped before sealing it is restored best-effort from what it recorded,
// with a warning, and without consulting Cloud.
func TestUnsealedArchiveRestoresLatestRecordedState(t *testing.T) {
	for _, state := range []archive.State{archive.StateInterrupted, archive.StateIncomplete} {
		t.Run(string(state), func(t *testing.T) {
			warnings := captureRestoreWarnings(t)
			chief, worker, edge, records := cloudControlFixture(t)
			// The worker was mid-turn when the engine died: RUNNING restores as IDLE.
			worker.State, worker.Failure = "RUNNING", ""
			records = append(records, chief.Record(), worker.Record(), edge.Record())
			source := &restoreTestArchive{
				bootstrapErr: &archive.RequestError{Kind: archive.ErrorState, Failure: archive.FailureState, State: state},
				unsealed:     &archive.UnsealedArchive{Cut: archive.HighWater{Logs: 1}},
				unsealedLogs: controlLogs(records...),
			}
			req := restoreRequest()
			req.source = source
			req.cloudSource = cloudRestoreFunc(func(context.Context, string, cloud.TraceImportSink) error {
				t.Fatal("a readable unsealed local archive must not fall back to Cloud")
				return nil
			})
			target := newFakeRestoreTarget()
			cleanup, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, target, req)
			require.NoError(t, err)
			cleanup()
			require.Contains(t, target.calls, "rehydrate:chief")
			require.Contains(t, target.calls, "rehydrate:worker")
			require.Contains(t, strings.Join(target.calls, "\n"), "subscribe:")
			require.Equal(t, "chief", target.focused)
			require.Contains(t, warnings.String(), "engine archive was not sealed")
			require.Contains(t, warnings.String(), string(state))
		})
	}
}

// TestUnsealedArchiveFallsBackToCloud: when an unsealed local archive cannot
// even produce a plan, Cloud is tried; if Cloud fails too, both failures are
// reported.
func TestUnsealedArchiveFallsBackToCloud(t *testing.T) {
	chief, worker, edge, records := cloudControlFixture(t)
	records = append(records, chief.Record(), worker.Record(), edge.Record())
	interrupted := &archive.RequestError{Kind: archive.ErrorState, Failure: archive.FailureState, State: archive.StateInterrupted}

	t.Run("cloud succeeds", func(t *testing.T) {
		req := restoreRequest()
		req.source = &restoreTestArchive{bootstrapErr: interrupted, unsealedErr: errors.New("store unreadable")}
		req.cloudSource = cloudRestoreFunc(func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
			return sink.ImportLogs(ctx, controlLogs(records...))
		})
		target := newFakeRestoreTarget()
		cleanup, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, target, req)
		require.NoError(t, err)
		cleanup()
		require.Contains(t, target.calls, "rehydrate:worker")
	})

	t.Run("cloud fails too", func(t *testing.T) {
		req := restoreRequest()
		req.source = &restoreTestArchive{bootstrapErr: interrupted, unsealedErr: errors.New("store unreadable")}
		req.cloudSource = cloudRestoreFunc(func(context.Context, string, cloud.TraceImportSink) error {
			return errors.New("cloud unreachable")
		})
		target := newFakeRestoreTarget()
		_, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, target, req)
		require.ErrorContains(t, err, "store unreadable")
		require.ErrorContains(t, err, "cloud unreachable")
		require.Empty(t, target.calls)
	})

	t.Run("sealed-state failures do not try unsealed", func(t *testing.T) {
		req := restoreRequest()
		source := &restoreTestArchive{
			bootstrapErr: &archive.RequestError{Kind: archive.ErrorState, Failure: archive.FailureState, State: archive.StateActive},
			unsealedErr:  errors.New("must not be called"),
		}
		req.source = source
		req.cloudSource = cloudRestoreFunc(func(context.Context, string, cloud.TraceImportSink) error {
			t.Fatal("a still-active archive is not a Cloud fallback case")
			return nil
		})
		_, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, newFakeRestoreTarget(), req)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "must not be called")
	})
}

func TestCloudRestoreSourceSelectionAndRemoval(t *testing.T) {
	chief, worker, edge, records := cloudControlFixture(t)
	// An older namespace sharing the trace, e.g. a nested session.
	other := chief
	other.Session, other.Activity = "other", time.Unix(1, 0)
	edge.Revision++
	edge.States = nil
	records = append(records, chief.Record(), worker.Record(), edge.Record(), other.Record())
	req := restoreRequest()
	req.source = &restoreTestArchive{bootstrapErr: archive.ErrCleanMiss}
	req.cloudSource = cloudRestoreFunc(func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
		return sink.ImportLogs(ctx, controlLogs(records...))
	})
	target := newFakeRestoreTarget()
	cleanup, err := restoreTraceSources(t.Context(), cloudTestFrontend{newRestoreTestFrontend()}, target, req)
	require.NoError(t, err)
	cleanup()
	require.Contains(t, target.calls, "rehydrate:worker", "the most recently active namespace is restored")
	require.NotContains(t, strings.Join(target.calls, "\n"), "subscribe:")
}

// TestCloudRestorePicksMostRecentNamespace: a trace carrying several
// namespaces restores the one with the most recent activity.
func TestCloudRestorePicksMostRecentNamespace(t *testing.T) {
	chief, worker, edge, records := cloudControlFixture(t)
	newer := chief
	newer.Session, newer.Activity = "nested", chief.Activity.Add(time.Hour)
	records = append(records, chief.Record(), worker.Record(), edge.Record(), newer.Record())
	fe := cloudTestFrontend{newRestoreTestFrontend()}
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Logs: fe.LogExporter()})
	require.NoError(t, importer.ImportLogs(t.Context(), controlLogs(records...)))
	plan, edges, err := observedRestorePlan(fe, restoreRequest(), nil)
	require.NoError(t, err)
	require.Len(t, plan.plan, 1)
	require.Equal(t, newer.Key, plan.plan[0].Source)
	require.Empty(t, edges)
}
