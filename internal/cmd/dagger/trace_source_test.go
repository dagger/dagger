package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/tracesource"
	"github.com/stretchr/testify/require"
)

// quietRuntimeError stands in for the driver's runtime-unavailable error: not
// a network error, and displayed alone (quietly) when found in a chain.
type quietRuntimeError struct{}

func (quietRuntimeError) Error() string {
	return "Dagger could not start the local engine using docker."
}

func (quietRuntimeError) Extensions() map[string]any {
	return map[string]any{"_quiet": true, "_message": "Dagger could not start the local engine using docker."}
}

// TestEngineTraceWithoutRuntimeFallsBackToCloud: on a machine without a
// container runtime the engine cannot be provisioned at all, so 'dagger
// trace' reads Cloud, as it did before trying the engine first.
func TestEngineTraceWithoutRuntimeFallsBackToCloud(t *testing.T) {
	noRuntime := func(context.Context) (*client.Client, error) { return nil, quietRuntimeError{} }
	local := func(ctx context.Context) (tracesource.Source, func() error, error) {
		return openEngineTraceWith(ctx, "2f123ba77bf7bd2d4db2f70ed20613e8", noRuntime)
	}

	t.Run("cloud serves", func(t *testing.T) {
		cloudOpened := false
		_, _, err := tracesource.Select(t.Context(), local, func(context.Context) (tracesource.Source, func() error, error) {
			cloudOpened = true
			return nil, nil, nil
		})
		require.NoError(t, err)
		require.True(t, cloudOpened)
	})

	t.Run("cloud fails", func(t *testing.T) {
		cloudErr := errors.New("cloud auth: not logged in")
		_, _, err := tracesource.Select(t.Context(), local, func(context.Context) (tracesource.Source, func() error, error) {
			return nil, nil, cloudErr
		})
		require.ErrorIs(t, err, cloudErr)
		require.ErrorContains(t, err, "could not start the local engine", "the engine failure must still be reported")
		var quiet interface{ Extensions() map[string]any }
		require.False(t, errors.As(err, &quiet), "a quiet engine error in the chain would hide the Cloud failure")
	})

	t.Run("connect timeout", func(t *testing.T) {
		// The client gives up on an unreachable runner with its own
		// deadline while the command's ctx is still live.
		timeout := func(ctx context.Context) (tracesource.Source, func() error, error) {
			return openEngineTraceWith(ctx, "2f123ba77bf7bd2d4db2f70ed20613e8", func(context.Context) (*client.Client, error) {
				return nil, fmt.Errorf("new client: %w", context.DeadlineExceeded)
			})
		}
		cloudOpened := false
		_, _, err := tracesource.Select(t.Context(), timeout, func(context.Context) (tracesource.Source, func() error, error) {
			cloudOpened = true
			return nil, nil, nil
		})
		require.NoError(t, err)
		require.True(t, cloudOpened)
	})

	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := tracesource.Select(ctx, local, func(context.Context) (tracesource.Source, func() error, error) {
			t.Fatal("a cancelled engine open must not fall back")
			return nil, nil, nil
		})
		require.Error(t, err)
	})
}

// TestEngineTraceParamsLeaveCloudURLAlone: the archive connection's Cloud URL
// (the setup page, with no Cloud telemetry configured) must never reach the
// frontend: it would enable the web key and `-w` on the setup page.
func TestEngineTraceParamsLeaveCloudURLAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DAGGER_CLOUD_TOKEN", "")
	prev := Frontend
	t.Cleanup(func() { Frontend = prev })
	Frontend = &idtui.FrontendMock{SetCloudURLFunc: func(_ context.Context, url string, _ string, _ bool) {
		t.Errorf("frontend Cloud URL set to %q", url)
	}}
	params, err := engineTraceParams(t.Context())
	require.NoError(t, err)
	require.Nil(t, params.CloudURLCallback,
		"client.Connect reports https://dagger.cloud/traces/setup through any callback")
}

// TestEngineTraceParamsNeverPrompt: reading an archive never prompts, and the
// non-pager --log dump has no running frontend to answer a prompt, even on a
// terminal.
func TestEngineTraceParamsNeverPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DAGGER_CLOUD_TOKEN", "")
	prevFrontend, prevTTY := Frontend, hasTTY
	t.Cleanup(func() { Frontend, hasTTY = prevFrontend, prevTTY })
	Frontend, hasTTY = &idtui.FrontendMock{}, true
	params, err := engineTraceParams(t.Context())
	require.NoError(t, err)
	require.Nil(t, params.PromptHandler)
}

// TestEngineTraceInsideSessionReadsCloud: inside a Dagger session the CLI
// would be a nested client, which the archive API always refuses (401, main
// client authority). The engine is skipped up front, so Cloud is read, while
// an authority refusal of a main client still stays final.
func TestEngineTraceInsideSessionReadsCloud(t *testing.T) {
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	cloudOpened := false
	_, _, err := tracesource.Select(t.Context(), func(ctx context.Context) (tracesource.Source, func() error, error) {
		return openEngineTrace(ctx, "2f123ba77bf7bd2d4db2f70ed20613e8")
	}, func(context.Context) (tracesource.Source, func() error, error) {
		cloudOpened = true
		return nil, nil, nil
	})
	require.NoError(t, err)
	require.True(t, cloudOpened, "a nested client reads Cloud")

	refused := &archive.RequestError{Kind: archive.ErrorTransient, StatusCode: http.StatusUnauthorized}
	_, _, err = tracesource.Select(t.Context(), func(context.Context) (tracesource.Source, func() error, error) {
		return nil, nil, refused
	}, func(context.Context) (tracesource.Source, func() error, error) {
		t.Fatal("an authority refusal must not fall back")
		return nil, nil, nil
	})
	require.ErrorIs(t, err, refused)
}
