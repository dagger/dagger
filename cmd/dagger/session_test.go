package main

import (
	"os"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/stretchr/testify/require"
)

func createTempGitRepo(t *testing.T) (string, func()) {
	tempDir, err := os.MkdirTemp("", "temp-git-repo")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	repo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("Failed to initialize temporary Git repository: %v", err)
	}

	// Get the current config
	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("Failed to get temporary Git repository configuration: %v", err)
	}

	// Set user name and email
	cfg.User.Name = "Test User"
	cfg.User.Email = "test@example.com"

	// Save the updated config
	err = repo.SetConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to set user name and email in temporary Git repository: %v", err)
	}

	// Set remote origin URL
	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{"https://github.com/test/test.git"},
	})
	if err != nil {
		t.Fatalf("Failed to set remote origin URL in temporary Git repository: %v", err)
	}

	// Clean up function
	cleanup := func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temporary directory: %v", err)
		}
	}

	return tempDir, cleanup
}

func TestGetGitInfo(t *testing.T) {
	tempDir, cleanup := createTempGitRepo(t)
	defer cleanup()

	// Change the working directory to the temporary Git repository
	origDir, err := os.Getwd()
	require.NoError(t, err, "Failed to get the current working directory")

	err = os.Chdir(tempDir)
	require.NoError(t, err, "Failed to change the working directory to the temporary Git repository")

	// Test inside a Git repository
	committerHash, repoHash, err := getGitInfo()
	require.NoError(t, err, "getGitInfo should not return an error inside a Git repository")
	require.Regexp(t, "^[a-fA-F0-9]{64}$", committerHash, "committerHash should be a valid hash-like string")
	require.Regexp(t, "^[a-fA-F0-9]{64}$", repoHash, "repoHash should be a valid hash-like string")

	// Restore the working directory before testing outside a Git repository
	err = os.Chdir(origDir)
	require.NoError(t, err, "Failed to restore the working directory")

	// Test outside a Git repository
	_, _, err = getGitInfo()
	require.Error(t, err, "getGitInfo should return an error outside a Git repository")
}

func TestSetupUserAgent(t *testing.T) {
	tempDir, cleanup := createTempGitRepo(t)

	// Change the working directory to the temporary Git repository
	origDir, err := os.Getwd()
	require.NoError(t, err, "Failed to get the current working directory")

	err = os.Chdir(tempDir)
	require.NoError(t, err, "Failed to change the working directory to the temporary Git repository")

	// Test setupUserAgent inside a Git repository
	userAgentCfg = userAgents{}
	setupUserAgent()
	require.NoError(t, err, "setupUserAgent should not return an error inside a Git repository")
	require.Contains(t, userAgentCfg.String(), "committer_hash:", "setupUserAgent should set committer_hash")
	require.Contains(t, userAgentCfg.String(), "repo_hash:", "setupUserAgent should set repo_hash")

	// Restore the working directory before testing outside a Git repository
	err = os.Chdir(origDir)
	require.NoError(t, err, "Failed to restore the working directory")

	// Clean up the temporary Git repository
	cleanup()

	// Test setupUserAgent outside a Git repository
	userAgentCfg = userAgents{}
	setupUserAgent()
	require.NoError(t, err, "setupUserAgent should not return an error outside a Git repository")
	require.NotContains(t, userAgentCfg.String(), "committer_hash:", "setupUserAgent should not set committer_hash")
	require.NotContains(t, userAgentCfg.String(), "repo_hash:", "setupUserAgent should not set repo_hash")
}
