package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A remote check whose request fails never runs remotely, so no remote span
// reports it. The failure must still surface as a failed check.
func TestRemoteCheckRequestFailureReportsFailedCheck(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(spans))
	ctx, root := tp.Tracer("test").Start(t.Context(), "root")
	defer root.End()

	// Without a workspace, the request fails before reaching a cloud engine.
	artifact := &Artifact{Path: []string{"lint"}}
	result, err := dagql.NewResultForCall(artifact, moduleObjectTestSyntheticCall("remoteArtifact", artifact))
	require.NoError(t, err)
	check := &Check{RemoteArtifact: dagql.ObjectResult[*Artifact]{Result: result}, Address: "dag://?check=lint"}
	require.ErrorContains(t, check.runRemote(ctx), "has no workspace")

	var reported []sdktrace.ReadOnlySpan
	for _, span := range spans.Ended() {
		for _, attr := range span.Attributes() {
			if string(attr.Key) == telemetry.CheckNameAttr && attr.Value.AsString() == check.Address {
				reported = append(reported, span)
			}
		}
	}
	require.Len(t, reported, 1)
	require.Equal(t, codes.Error, reported[0].Status().Code)
	passed := false
	for _, attr := range reported[0].Attributes() {
		if string(attr.Key) == telemetry.CheckPassedAttr {
			passed = true
			require.False(t, attr.Value.AsBool())
		}
	}
	require.True(t, passed, "the failed check reports whether it passed")
}
