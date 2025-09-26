package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewFormatStructure verifies the basic structure of the new tuple format
func TestNewFormatStructure(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}

	// Add a single entry
	l.entries[l.makeKey("core", "container.from", `{"ref":"alpine:latest"}`)] = &Entry{
		Module:   "core",
		Function: "container.from",
		Inputs:   json.RawMessage(`{"ref":"alpine:latest"}`),
		Output:   "sha256:abc123",
	}
	l.dirty = true

	// Save and verify format
	err := l.Save()
	require.NoError(t, err)

	content, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 2, "Should have version header + 1 entry")

	// Verify version header format
	var header [][]string
	err = json.Unmarshal([]byte(lines[0]), &header)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"version", "1"}}, header)

	// Verify entry is a 4-element tuple
	var entry []json.RawMessage
	err = json.Unmarshal([]byte(lines[1]), &entry)
	require.NoError(t, err)
	require.Len(t, entry, 4, "Entry should have exactly 4 elements")

	// Verify each element
	var module, function, output string
	var inputs []interface{}

	require.NoError(t, json.Unmarshal(entry[0], &module))
	require.NoError(t, json.Unmarshal(entry[1], &function))
	require.NoError(t, json.Unmarshal(entry[2], &inputs))
	require.NoError(t, json.Unmarshal(entry[3], &output))

	require.Equal(t, "core", module)
	require.Equal(t, "container.from", function)
	require.Equal(t, []interface{}{"alpine:latest"}, inputs)
	require.Equal(t, "sha256:abc123", output)
}

// TestInputFieldOrdering verifies deterministic ordering of input fields
func TestInputFieldOrdering(t *testing.T) {
	l := &LockfileAttachable{}

	testCases := []struct {
		name     string
		inputs   map[string]interface{}
		expected []interface{}
	}{
		{
			name: "git with repo and ref",
			inputs: map[string]interface{}{
				"ref":  "main",
				"repo": "https://github.com/dagger/dagger",
			},
			expected: []interface{}{"https://github.com/dagger/dagger", "main"},
		},
		{
			name: "container.from with ref",
			inputs: map[string]interface{}{
				"ref": "alpine:latest",
			},
			expected: []interface{}{"alpine:latest"},
		},
		{
			name: "http.get with url",
			inputs: map[string]interface{}{
				"url": "https://example.com/file.tar.gz",
			},
			expected: []interface{}{"https://example.com/file.tar.gz"},
		},
		{
			name: "unknown function with multiple fields",
			inputs: map[string]interface{}{
				"zebra": "last",
				"alpha": "first",
				"beta":  "second",
			},
			expected: []interface{}{"first", "second", "last"}, // alphabetical order
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := l.inputsToArray(tc.inputs)
			require.NoError(t, err)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestRoundTrip verifies that entries survive save/load cycle
func TestRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	originalEntries := map[string]*Entry{
		"entry1": {
			Module:   "core",
			Function: "container.from",
			Inputs:   json.RawMessage(`{"ref":"alpine:latest"}`),
			Output:   "sha256:abc123",
		},
		"entry2": {
			Module:   "core",
			Function: "http.get",
			Inputs:   json.RawMessage(`{"url":"https://example.com"}`),
			Output:   "sha256:def456",
		},
		"entry3": {
			Module:   "core",
			Function: "git.branch",
			Inputs:   json.RawMessage(`{"ref":"main","repo":"https://github.com/dagger/dagger"}`),
			Output:   "commit:xyz789",
		},
	}

	// Save
	l1 := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
		dirty:   true,
	}

	for _, entry := range originalEntries {
		key := l1.makeKey(entry.Module, entry.Function, string(entry.Inputs))
		l1.entries[key] = entry
	}

	err := l1.Save()
	require.NoError(t, err)

	// Load
	l2 := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}
	err = l2.load()
	require.NoError(t, err)

	// Verify all entries are present
	require.Len(t, l2.entries, len(originalEntries))

	// Verify each entry
	for _, original := range originalEntries {
		key := l2.makeKey(original.Module, original.Function, string(original.Inputs))
		loaded, exists := l2.entries[key]
		require.True(t, exists, fmt.Sprintf("Entry missing for %s.%s", original.Module, original.Function))
		require.Equal(t, original.Output, loaded.Output)
	}
}

// TestVersionHeader verifies version header handling
func TestVersionHeader(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	// Test missing version header
	invalidContent := `["core","container.from",["alpine:latest"],"sha256:abc123"]`
	err := os.WriteFile(lockfilePath, []byte(invalidContent), 0644)
	require.NoError(t, err)

	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}
	err = l.load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing version header")

	// Test unsupported version
	unsupportedContent := `[["version","2"]]
["core","container.from",["alpine:latest"],"sha256:abc123"]`
	err = os.WriteFile(lockfilePath, []byte(unsupportedContent), 0644)
	require.NoError(t, err)

	l = &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}
	err = l.load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported lockfile version")
}

// TestEmptyLockfile verifies handling of empty/missing lockfiles
func TestEmptyLockfile(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "dagger.lock")

	// Test loading non-existent file
	l := &LockfileAttachable{
		entries: make(map[string]*Entry),
		path:    lockfilePath,
	}
	err := l.load()
	require.NoError(t, err, "Loading non-existent file should not error")
	require.Empty(t, l.entries)

	// Test saving empty lockfile
	l.dirty = true
	err = l.Save()
	require.NoError(t, err)

	// Verify it creates a valid empty lockfile with just version header
	content, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)
	require.Equal(t, `[["version","1"]]`+"\n", string(content))
}
