package session

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

// High-level test strategy:
// 1. Use Dagger to create isolated container environments for testing
// 2. Mount gitcredential implementation + generated proto inside container as the session package
// 3. Run tests and collect output through container stdout
func TestGitCredentialProto(t *testing.T) {
	tests := []struct {
		name             string
		setup            func(*dagger.Container) *dagger.Container
		request          *GitCredentialRequest
		expectedError    ErrorInfo_ErrorType
		expectedReason   string
		expectedResponse *CredentialInfo
	}{
		// Test case 1: Happy path - credential helper returns valid credentials
		// Verifies that when a credential helper returns properly formatted credentials,
		// they are correctly parsed and returned
		{
			name: "VALID_CREDENTIALS",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithNewFile("/usr/local/bin/git-credential-valid", `#!/bin/sh
echo "protocol=https"
echo "host=github.com"
echo "username=testuser"
echo "password=testpass"
`).
					WithExec([]string{"chmod", "+x", "/usr/local/bin/git-credential-valid"}).
					WithExec([]string{"git", "config", "--global", "credential.helper", "valid"})
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "github.com",
			},
			expectedResponse: &CredentialInfo{
				Protocol: "https",
				Host:     "github.com",
				Username: "testuser",
				Password: "testpass",
			},
		},
		{
			// Test case 2: Input validation - empty required fields
			// Verifies that the service properly validates input before attempting
			// to query Git
			name: "INVALID_REQUEST",
			request: &GitCredentialRequest{
				Protocol: "",
				Host:     "",
			},
			expectedError:  INVALID_REQUEST,
			expectedReason: "Host and protocol are required",
		},
		{
			// Test case 3: Environment check - Git not available
			// Verifies that the service properly handles cases where Git is not
			// installed or not in PATH
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
			// Test case 4: Malformed helper output
			// Verifies that invalid output format from credential helper
			// is properly handled as a credential retrieval failure
			name: "INVALID_FORMAT_FROM_HELPER",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithNewFile("/usr/local/bin/git-credential-invalid", `#!/bin/sh
while read line; do
    case "$line" in
        "") break ;;
    esac
done
echo "this is not a key value pair"
exit 1
`).
					WithExec([]string{"chmod", "+x", "/usr/local/bin/git-credential-invalid"}).
					WithExec([]string{"git", "config", "--global", "credential.helper", "invalid"}).
					// Prevent Git from falling back to interactive prompts in no-tty environment
					WithEnvVariable("GIT_ASKPASS", "")
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "github.com",
			},
			expectedError:  CREDENTIAL_RETRIEVAL_FAILED,
			expectedReason: "Failed to retrieve credentials: exit status 128",
		},
		{
			// Test case 5: No credentials found
			// Verifies that when Git can't find credentials, it's handled as
			// a credential retrieval failure (Git's standard behavior)
			name: "MISSING_CREDENTIALS",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithNewFile("/usr/local/bin/git-credential-missing", `#!/bin/sh
# Read input silently
while read line; do
    case "$line" in
        "") break ;;
    esac
done
# Exit with status 128 to indicate no credentials found
# This is Git's expected behavior when no credentials are found
exit 128
`).
					WithExec([]string{"chmod", "+x", "/usr/local/bin/git-credential-missing"}).
					WithExec([]string{"git", "config", "--global", "credential.helper", "missing"}).
					WithEnvVariable("GIT_ASKPASS", "").
					WithEnvVariable("GIT_TERMINAL_PROMPT", "0")
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "github.com",
			},
			expectedError:  CREDENTIAL_RETRIEVAL_FAILED,
			expectedReason: "Failed to retrieve credentials: exit status 128",
		},
		{
			// Test case 6: Timeout handling
			// Verifies that the service properly handles credential helpers
			// that take too long to respond
			name: "TIMEOUT",
			setup: func(c *dagger.Container) *dagger.Container {
				return c.WithNewFile("/usr/local/bin/git-credential-slow", `#!/bin/sh
# Read all input first
while read line; do
    case "$line" in
        "") break ;;
    esac
done
# Sleep longer than the 30s timeout
sleep 31
`).
					WithExec([]string{"chmod", "+x", "/usr/local/bin/git-credential-slow"}).
					WithExec([]string{"git", "config", "--global", "credential.helper", "slow"})
			},
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "github.com",
			},
			expectedError:  TIMEOUT,
			expectedReason: "Git credential command timed out",
		},
	}

	// setup dagger
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)
	defer client.Close()

	// Create base container with all dependencies
	baseContainer := client.Container().
		From("golang:1.21").
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{"apt-get", "install", "-y", "git"}).
		WithExec([]string{"mkdir", "-p", "/app/session"}).
		WithWorkdir("/app").
		// create go.mod so that below main() can test our proto handling
		WithNewFile("/app/go.mod", `
module testapp

go 1.21

require (
    github.com/gogo/protobuf v1.3.2
    google.golang.org/grpc v1.59.0
)
`).
		// Mount gitcredential implementation as the session pkg
		WithMountedFile("/app/session/gitcredential.pb.go", client.Host().File("./gitcredential.pb.go")).
		WithMountedFile("/app/session/gitcredential.go", client.Host().File("./gitcredential.go")).
		WithNewFile("/app/session/package.go", `package session`).

		// Create test harness that:
		// 1. Reads request from JSON file
		// 2. Calls our implementation
		// 3. Outputs response as JSON
		WithNewFile("/app/test.go", `
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io/ioutil"

    "testapp/session"
)

func main() {
    data, err := ioutil.ReadFile("/request.json")
    if err != nil {
        panic(err)
    }

        var request session.GitCredentialRequest
    if err := json.Unmarshal(data, &request); err != nil {
        panic(err)
    }

    s := session.NewGitCredentialAttachable(context.Background())
    response, err := s.GetCredential(context.Background(), &request)
    if err != nil {
        panic(err)
    }

    responseJSON, err := json.Marshal(response)
    if err != nil {
        panic(err)
    }
    fmt.Println(string(responseJSON))
}
`).
		WithNewFile("/request.json", "{}").
		WithWorkdir("/app").
		WithExec([]string{"go", "mod", "tidy"})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Start from the base container
			container := baseContainer

			// Apply test-specific setup
			if tt.setup != nil {
				container = tt.setup(container)
			}

			// Create a file with the request
			requestJSON, err := json.Marshal(tt.request)
			require.NoError(t, err)

			container = container.
				WithNewFile("/request.json", string(requestJSON)).
				WithExec([]string{"go", "run", "test.go"})

			// assert response
			output, err := container.Stdout(ctx)
			require.NoError(t, err)

			t.Logf("Raw output: %s", output)

			var wrapper struct {
				Result struct {
					Error struct {
						Type    ErrorInfo_ErrorType `json:"type"`
						Message string              `json:"message"`
					} `json:"error"`
				} `json:"Result"`
			}

			err = json.Unmarshal([]byte(output), &wrapper)
			require.NoError(t, err)

			// Check error response
			require.Equal(t, tt.expectedError, wrapper.Result.Error.Type)
			require.Equal(t, tt.expectedReason, wrapper.Result.Error.Message)
		})
	}
}
