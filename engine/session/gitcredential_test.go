package session

import (
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TODO(guillaume): to be run inside a container
const (
	gitPath         = "/usr/local/bin/git"
	homeDir         = "/root"
	gitConfigPath   = homeDir + "/.gitconfig"
	credentialsPath = homeDir + "/.git-credentials"
)

func TestGitCredentialErrors(t *testing.T) {
	tests := []struct {
		name           string
		setup          func() error
		cleanup        func()
		request        *GitCredentialRequest
		expectedError  ErrorInfo_ErrorType
		expectedReason string
	}{
		{
			name: "INVALID_REQUEST",
			request: &GitCredentialRequest{
				Protocol: "",
				Host:     "",
			},
			expectedError:  INVALID_REQUEST,
			expectedReason: "Host and protocol are required",
		},
		{
			name: "NO_GIT",
			setup: func() error {
				// Temporarily rename git executable
				return os.Rename("/usr/bin/git", "/usr/bin/git_temp")
			},
			cleanup: func() {
				os.Rename("/usr/bin/git_temp", "/usr/bin/git")
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
			setup: func() error {
				cmd := exec.Command("git", "config", "--global", "--unset", "credential.helper")
				cmd.Env = os.Environ()
				return cmd.Run()
			},
			cleanup: func() {
				cmd := exec.Command("git", "config", "--global", "--unset", "credential.helper")
				cmd.Env = os.Environ()
				cmd.Run()
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
			setup: func() error {
				// Create a git credential helper that sleeps
				err := os.WriteFile("/tmp/slow_helper.sh", []byte("#!/bin/sh\nsleep 11\n"), 0755)
				if err != nil {
					return err
				}
				// Configure git to use our slow helper as credential helper
				cmd := exec.Command("git", "config", "--global", "credential.helper", "/tmp/slow_helper.sh")
				return cmd.Run()
			},
			cleanup: func() {
				os.Remove("/tmp/slow_helper.sh")
				exec.Command("git", "config", "--global", "--unset", "credential.helper").Run()
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
			request: &GitCredentialRequest{
				Protocol: "https",
				Host:     "nonexistent.com",
			},
			expectedError:  CREDENTIAL_RETRIEVAL_FAILED,
			expectedReason: "Failed to retrieve credentials: exit status 128",
		},
	}

	s := NewGitCredentialAttachable(context.TODO())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary directory
			tempDir, err := ioutil.TempDir("", "git_test")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tempDir)

			// Set HOME and GIT_CONFIG_GLOBAL to use tempDir
			os.Setenv("HOME", tempDir)
			os.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(tempDir, ".gitconfig"))
			defer func() {
				os.Unsetenv("HOME")
				os.Unsetenv("GIT_CONFIG_GLOBAL")
			}()

			// Ensure git commands use the updated environment
			if tt.name != "NO_GIT" {
				cmd := exec.Command("git", "config", "--global", "credential.helper", "store")
				cmd.Env = os.Environ()
				if err := cmd.Run(); err != nil {
					t.Fatalf("Failed to set credential.helper: %v", err)
				}
			}

			if tt.setup != nil {
				if err := tt.setup(); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}
			if tt.cleanup != nil {
				defer tt.cleanup()
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			response, err := s.GetCredential(ctx, tt.request)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if errorResponse, ok := response.Result.(*GitCredentialResponse_Error); ok {
				if errorResponse.Error.Type != tt.expectedError {
					t.Errorf("Expected error type %v, got %v", tt.expectedError, errorResponse.Error.Type)
				}
				if errorResponse.Error.Message != tt.expectedReason {
					t.Errorf("Expected error message '%s', got '%s'", tt.expectedReason, errorResponse.Error.Message)
				}
			} else {
				t.Errorf("Expected error response, got success")
			}
		})
	}
}
