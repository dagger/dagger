package lockfile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLockfileLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	// Create a lockfile attachable
	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}

	// Add some entries
	l.entries[l.makeKey("core", "container.from", `{"ref":"alpine:latest"}`)] = &Entry{
		Module:   "core",
		Function: "container.from",
		Inputs:   json.RawMessage(`{"ref":"alpine:latest"}`),
		Output:   "sha256:abc123",
	}

	l.entries[l.makeKey("core", "container.from", `{"ref":"ubuntu:22.04"}`)] = &Entry{
		Module:   "core",
		Function: "container.from",
		Inputs:   json.RawMessage(`{"ref":"ubuntu:22.04"}`),
		Output:   "sha256:def456",
	}

	l.dirty = true

	// Save the lockfile
	err := l.Save()
	require.NoError(t, err)

	// Verify file exists
	_, err = os.Stat(lockfilePath)
	require.NoError(t, err)

	// Verify the tuple format
	content, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 3) // Version header + 2 entries

	// Check version header
	require.Equal(t, `[["version","1"]]`, lines[0])

	// Check that entries are in tuple format
	require.Contains(t, string(content), `["core","container.from",["alpine:latest"],"sha256:abc123"]`)
	require.Contains(t, string(content), `["core","container.from",["ubuntu:22.04"],"sha256:def456"]`)

	// Load into a new instance
	l2 := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}
	err = l2.load()
	require.NoError(t, err)

	// Verify entries were loaded correctly
	require.Len(t, l2.entries, 2)

	// Check specific entries - note that inputs are reconstructed
	key1 := l2.makeKey("core", "container.from", `{"ref":"alpine:latest"}`)
	entry1, exists := l2.entries[key1]
	require.True(t, exists)
	require.Equal(t, "sha256:abc123", entry1.Output)

	key2 := l2.makeKey("core", "container.from", `{"ref":"ubuntu:22.04"}`)
	entry2, exists := l2.entries[key2]
	require.True(t, exists)
	require.Equal(t, "sha256:def456", entry2.Output)
}

func TestLockfileGetSet(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}

	ctx := context.Background()

	// Test cache miss
	resp, err := l.Get(ctx, &GetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"alpine:latest"}`,
	})
	require.NoError(t, err)
	require.Equal(t, "", resp.Output)

	// Set a value
	_, err = l.Set(ctx, &SetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"alpine:latest"}`,
		Output:   "sha256:abc123",
	})
	require.NoError(t, err)

	// Test cache hit
	resp, err = l.Get(ctx, &GetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"alpine:latest"}`,
	})
	require.NoError(t, err)
	require.Equal(t, "sha256:abc123", resp.Output)

	// Test different input gives cache miss
	resp, err = l.Get(ctx, &GetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"ubuntu:22.04"}`,
	})
	require.NoError(t, err)
	require.Equal(t, "", resp.Output)
}

func TestLockfileGlobalHelpers(t *testing.T) {
	// Set up a global lockfile
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	globalMutex.Lock()
	globalLockfile = &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}
	globalMutex.Unlock()

	// Test cache miss
	result := GetGlobal("core", "container.from", map[string]interface{}{
		"ref": "alpine:latest",
	})
	require.Equal(t, "", result)

	// Set a value
	SetGlobal("core", "container.from", map[string]interface{}{
		"ref": "alpine:latest",
	}, "sha256:abc123")

	// Test cache hit
	result = GetGlobal("core", "container.from", map[string]interface{}{
		"ref": "alpine:latest",
	})
	require.Equal(t, "sha256:abc123", result)

	// Test different input gives cache miss
	result = GetGlobal("core", "container.from", map[string]interface{}{
		"ref": "ubuntu:22.04",
	})
	require.Equal(t, "", result)

	// Clean up
	globalMutex.Lock()
	globalLockfile = nil
	globalMutex.Unlock()
}

func TestLockfileSorting(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}

	// Add entries in random order
	l.entries[l.makeKey("zeta", "func", `{}`)] = &Entry{
		Module:   "zeta",
		Function: "func",
		Inputs:   json.RawMessage(`{}`),
		Output:   "output1",
	}

	l.entries[l.makeKey("alpha", "func2", `{}`)] = &Entry{
		Module:   "alpha",
		Function: "func2",
		Inputs:   json.RawMessage(`{}`),
		Output:   "output2",
	}

	l.entries[l.makeKey("alpha", "func1", `{}`)] = &Entry{
		Module:   "alpha",
		Function: "func1",
		Inputs:   json.RawMessage(`{}`),
		Output:   "output3",
	}

	l.dirty = true

	// Save and check file content for ordering
	err := l.Save()
	require.NoError(t, err)

	// Read file content
	content, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 4) // Version header + 3 entries

	// Verify version header
	require.Equal(t, `[["version","1"]]`, lines[0])

	// Verify ordering: alpha.func1, alpha.func2, zeta.func
	require.Contains(t, lines[1], `"alpha","func1"`)
	require.Contains(t, lines[2], `"alpha","func2"`)
	require.Contains(t, lines[3], `"zeta","func"`)
}

func TestLockfileDeduplication(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}

	ctx := context.Background()

	// Set initial value
	_, err := l.Set(ctx, &SetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"alpine:latest"}`,
		Output:   "sha256:old",
	})
	require.NoError(t, err)
	require.True(t, l.dirty)

	// Reset dirty flag
	l.dirty = false

	// Set same value - should not mark as dirty
	_, err = l.Set(ctx, &SetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"alpine:latest"}`,
		Output:   "sha256:old",
	})
	require.NoError(t, err)
	require.False(t, l.dirty)

	// Update with new value - should mark as dirty
	_, err = l.Set(ctx, &SetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"alpine:latest"}`,
		Output:   "sha256:new",
	})
	require.NoError(t, err)
	require.True(t, l.dirty)

	// Verify only one entry exists
	require.Len(t, l.entries, 1)

	// Verify it has the new value
	resp, err := l.Get(ctx, &GetRequest{
		Module:   "core",
		Function: "container.from",
		Inputs:   `{"ref":"alpine:latest"}`,
	})
	require.NoError(t, err)
	require.Equal(t, "sha256:new", resp.Output)
}

func TestFindLockfilePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory structure
	projectDir := filepath.Join(tmpDir, "project")
	subDir := filepath.Join(projectDir, "subdir")
	err := os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	l := &LockfileAttachable{}

	// Test 1: No dagger.json, use current directory
	path := l.findLockfilePath(subDir)
	require.Equal(t, filepath.Join(subDir, "dagger.lock"), path)

	// Test 2: dagger.json in parent directory
	daggerJSONPath := filepath.Join(projectDir, "dagger.json")
	err = os.WriteFile(daggerJSONPath, []byte("{}"), 0644)
	require.NoError(t, err)

	path = l.findLockfilePath(subDir)
	require.Equal(t, filepath.Join(projectDir, "dagger.lock"), path)

	// Test 3: dagger.json in current directory
	subDaggerJSONPath := filepath.Join(subDir, "dagger.json")
	err = os.WriteFile(subDaggerJSONPath, []byte("{}"), 0644)
	require.NoError(t, err)

	path = l.findLockfilePath(subDir)
	require.Equal(t, filepath.Join(subDir, "dagger.lock"), path)
}

func TestTupleFormat(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	// Test various input types
	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}

	// Add entries with different input formats
	l.entries[l.makeKey("core", "container.from", `{"ref":"alpine:latest"}`)] = &Entry{
		Module:   "core",
		Function: "container.from",
		Inputs:   json.RawMessage(`{"ref":"alpine:latest"}`),
		Output:   "sha256:abc123",
	}

	l.entries[l.makeKey("core", "http.get", `{"url":"https://example.com/file.tar.gz"}`)] = &Entry{
		Module:   "core",
		Function: "http.get",
		Inputs:   json.RawMessage(`{"url":"https://example.com/file.tar.gz"}`),
		Output:   "sha256:xyz789",
	}

	l.entries[l.makeKey("core", "git.branch", `{"ref":"main","repo":"https://github.com/dagger/dagger"}`)] = &Entry{
		Module:   "core",
		Function: "git.branch",
		Inputs:   json.RawMessage(`{"ref":"main","repo":"https://github.com/dagger/dagger"}`),
		Output:   "abc123def456",
	}

	l.dirty = true

	// Save the lockfile
	err := l.Save()
	require.NoError(t, err)

	// Read and verify content
	content, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Equal(t, `[["version","1"]]`, lines[0])

	// Verify tuple format is compact
	require.Contains(t, string(content), `["core","container.from",["alpine:latest"],"sha256:abc123"]`)
	require.Contains(t, string(content), `["core","http.get",["https://example.com/file.tar.gz"],"sha256:xyz789"]`)
	require.Contains(t, string(content), `["core","git.branch",["https://github.com/dagger/dagger","main"],"abc123def456"]`)

	// Load and verify
	l2 := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}
	err = l2.load()
	require.NoError(t, err)

	// Verify all entries loaded correctly
	require.Len(t, l2.entries, 3)

	// Check container.from entry
	key1 := l2.makeKey("core", "container.from", `{"ref":"alpine:latest"}`)
	entry1, exists := l2.entries[key1]
	require.True(t, exists)
	require.Equal(t, "sha256:abc123", entry1.Output)

	// Check http.get entry
	key2 := l2.makeKey("core", "http.get", `{"url":"https://example.com/file.tar.gz"}`)
	entry2, exists := l2.entries[key2]
	require.True(t, exists)
	require.Equal(t, "sha256:xyz789", entry2.Output)

	// Check git.branch entry - note JSON keys are alphabetized by json.Marshal
	key3 := l2.makeKey("core", "git.branch", `{"ref":"main","repo":"https://github.com/dagger/dagger"}`)
	entry3, exists := l2.entries[key3]
	require.True(t, exists)
	require.Equal(t, "abc123def456", entry3.Output)
}

func TestInputsToArray(t *testing.T) {
	l := &LockfileAttachable{}

	tests := []struct {
		name     string
		inputs   interface{}
		expected []interface{}
	}{
		{
			name:     "container.from with ref",
			inputs:   map[string]interface{}{"ref": "alpine:latest"},
			expected: []interface{}{"alpine:latest"},
		},
		{
			name:     "container.from with image",
			inputs:   map[string]interface{}{"image": "ubuntu:22.04"},
			expected: []interface{}{"ubuntu:22.04"},
		},
		{
			name:     "http.get with url",
			inputs:   map[string]interface{}{"url": "https://example.com"},
			expected: []interface{}{"https://example.com"},
		},
		{
			name:     "git with repo and ref",
			inputs:   map[string]interface{}{"repo": "https://github.com/dagger/dagger", "ref": "main"},
			expected: []interface{}{"https://github.com/dagger/dagger", "main"},
		},
		{
			name:     "empty inputs",
			inputs:   nil,
			expected: []interface{}{},
		},
		{
			name:     "string input",
			inputs:   "single-value",
			expected: []interface{}{"single-value"},
		},
		{
			name:     "array input",
			inputs:   []interface{}{"a", "b", "c"},
			expected: []interface{}{"a", "b", "c"},
		},
		{
			name:     "mixed fields with ordering",
			inputs:   map[string]interface{}{"zebra": "last", "alpha": "first", "ref": "priority"},
			expected: []interface{}{"priority", "first", "last"}, // ref first, then alphabetical
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := l.inputsToArray(tt.inputs)
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestArrayToInputs(t *testing.T) {
	l := &LockfileAttachable{}

	tests := []struct {
		name     string
		module   string
		function string
		array    []interface{}
		expected map[string]interface{}
	}{
		{
			name:     "container.from",
			module:   "core",
			function: "container.from",
			array:    []interface{}{"alpine:latest"},
			expected: map[string]interface{}{"ref": "alpine:latest"},
		},
		{
			name:     "http.get",
			module:   "core",
			function: "http.get",
			array:    []interface{}{"https://example.com"},
			expected: map[string]interface{}{"url": "https://example.com"},
		},
		{
			name:     "git.branch",
			module:   "core",
			function: "git.branch",
			array:    []interface{}{"https://github.com/dagger/dagger", "main"},
			expected: map[string]interface{}{"repo": "https://github.com/dagger/dagger", "ref": "main"},
		},
		{
			name:     "empty array",
			module:   "core",
			function: "something",
			array:    []interface{}{},
			expected: map[string]interface{}{},
		},
		{
			name:     "unknown function single value",
			module:   "custom",
			function: "unknown",
			array:    []interface{}{"value"},
			expected: map[string]interface{}{"value": "value"},
		},
		{
			name:     "unknown function multiple values",
			module:   "custom",
			function: "unknown",
			array:    []interface{}{"a", "b", "c"},
			expected: map[string]interface{}{"arg0": "a", "arg1": "b", "arg2": "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := l.arrayToInputs(tt.module, tt.function, tt.array)
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}
