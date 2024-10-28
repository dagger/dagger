package session

import (
	"context"
	"encoding/json"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

func connect(ctx context.Context, t *testctx.T, opts ...dagger.ClientOpt) *dagger.Client {
	// opts = append([]dagger.ClientOpt{
	// 	dagger.WithLogOutput(testutil.NewTWriter(t.T)),
	// }, opts...)
	client, err := dagger.Connect(ctx, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

var testCtx = context.Background()

type GitCredentialSuite struct{}

func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationLibrary)
}

func Logger() log.Logger {
	return telemetry.Logger(testCtx, InstrumentationLibrary)
}

func Middleware() []testctx.Middleware {
	return []testctx.Middleware{
		testctx.WithParallel,
		testctx.WithOTelLogging(Logger()),
		testctx.WithOTelTracing(Tracer()),
	}
}

func TestGitCredential(t *testing.T) {
	testctx.Run(testCtx, t, GitCredentialSuite{}, Middleware()...)
}

// This tests the proto, but having a hard time implementing the test
func (GitCredentialSuite) TestGitCredentialErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tests := []struct {
		name           string
		setup          func(*dagger.Container) *dagger.Container
		request        *GitCredentialRequest
		expectedError  ErrorInfo_ErrorType
		expectedReason string
	}{
		{
			name: "INVALID_REQUEST",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithEnvVariable("HOME", "/root").
					WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig")
			},
			request: &GitCredentialRequest{
				Protocol: "",
				Host:     "",
			},
			expectedError:  INVALID_REQUEST,
			expectedReason: "Host and protocol are required",
		},
		{
			name: "NO_GIT",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithExec([]string{"mv", "/usr/bin/git", "/usr/bin/git_temp"})
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "github.com",
			},
			expectedError:  NO_GIT,
			expectedReason: "Git is not installed or not in PATH",
		},
		{
			name: "NO_CREDENTIAL_HELPER",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithEnvVariable("HOME", "/root").
					WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig").
					WithExec([]string{"git", "config", "--global", "--unset", "credential.helper", "||", "true"})
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "github.com",
			},
			expectedError:  CREDENTIAL_RETRIEVAL_FAILED,
			expectedReason: "Failed to retrieve credentials: exit status 128",
		},
		{
			name: "TIMEOUT",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithEnvVariable("HOME", "/root").
					WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig").
					WithNewFile("/tmp/slow_helper.sh", `#!/bin/sh
sleep 31
`).
					WithExec([]string{"chmod", "+x", "/tmp/slow_helper.sh"}).
					WithExec([]string{"git", "config", "--global", "credential.helper", "/tmp/slow_helper.sh"})
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "github.com",
			},
			expectedError:  TIMEOUT,
			expectedReason: "Git credential command timed out",
		},
		{
			name: "CREDENTIAL_RETRIEVAL_FAILED",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithEnvVariable("HOME", "/root").
					WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig")
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "nonexistent.com",
			},
			expectedError:  CREDENTIAL_RETRIEVAL_FAILED,
			expectedReason: "Failed to retrieve credentials: exit status 128",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			// Setup base container
			container := c.Container().
				From("golang:1.16").
				WithExec([]string{"apt-get", "update"}).
				WithExec([]string{"apt-get", "install", "-y", "git"})

			// Apply test-specific setup
			if tt.setup != nil {
				container = tt.setup(container)
			}

			// Create a file with the request
			requestJSON, err := json.Marshal(tt.request)
			require.NoError(t, err)

			container = container.
				WithNewFile("/request.json", string(requestJSON)).
				// Copy the git credential implementation into the container
				WithMountedFile("/git_credential.go", c.Host().File("./git_credential.go"))

			// Create a test program to run the git credential implementation
			testProgram := `
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "os"
)

func main() {
    // Read request
    data, err := ioutil.ReadFile("/request.json")
    if err != nil {
        panic(err)
    }

    var request GitCredentialRequest
    if err := json.Unmarshal(data, &request); err != nil {
        panic(err)
    }

    // Create GitCredentialAttachable and run test
    s := NewGitCredentialAttachable(context.Background())
    response, err := s.GetCredential(context.Background(), &request)
    if err != nil {
        panic(err)
    }

    // Write response
    responseJSON, err := json.Marshal(response)
    if err != nil {
        panic(err)
    }
    fmt.Println(string(responseJSON))
}
`
			container = container.
				WithNewFile("/test.go", testProgram).
				WithExec([]string{"go", "run", "/test.go"})

			// Get the output and parse the response
			output, err := container.Stdout(ctx)
			require.NoError(t, err)

			var response GitCredentialResponse
			err = json.Unmarshal([]byte(output), &response)
			require.NoError(t, err)

			// Check error response
			errorResponse, ok := response.Result.(*GitCredentialResponse_Error)
			require.True(t, ok, "Expected error response, got success")
			require.Equal(t, tt.expectedError, errorResponse.Error.Type)
			require.Equal(t, tt.expectedReason, errorResponse.Error.Message)
		})
	}
}
