package lockfile

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBasicOperations(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create new lockfile
	lf := New()

	// Test initial state
	require.False(t, lf.IsDirty(), "New lockfile should not be dirty")

	// Add an entry
	args := []FunctionArg{
		{Name: "ref", Value: "alpine:latest"},
	}
	err := lf.Set("core", "container.from", args, "sha256:abc123")
	require.NoError(t, err, "Failed to set entry")

	require.True(t, lf.IsDirty(), "Lockfile should be dirty after Set")

	// Get the entry back
	result := lf.Get("core", "container.from", args)
	require.Equal(t, "sha256:abc123", result)

	// Get non-existent entry
	result = lf.Get("core", "container.from", []FunctionArg{
		{Name: "ref", Value: "ubuntu:latest"},
	})
	require.Nil(t, result, "Expected nil for non-existent entry")

	// Save
	require.NoError(t, lf.Save(lockPath), "Failed to save")

	require.False(t, lf.IsDirty(), "Lockfile should not be dirty after save")

	// Load
	lf2, err := Load(lockPath)
	require.NoError(t, err, "Failed to load")

	// Verify loaded data
	result = lf2.Get("core", "container.from", args)
	require.Equal(t, "sha256:abc123", result, "After load: expected sha256:abc123")
}

func TestArgumentOrder(t *testing.T) {
	lf := New()

	// Set with specific argument order
	args1 := []FunctionArg{
		{Name: "url", Value: "https://example.com"},
		{Name: "method", Value: "GET"},
		{Name: "timeout", Value: 30},
	}
	// Use a deterministic result type (array instead of map)
	err := lf.Set("http", "request", args1, []interface{}{
		200,
		"success",
	})
	require.NoError(t, err, "Should set successfully with deterministic types")

	// Get with same order - should find it
	result := lf.Get("http", "request", args1)
	require.NotNil(t, result, "Should find entry with same argument order")

	// Get with different order - should NOT find it
	args2 := []FunctionArg{
		{Name: "method", Value: "GET"},
		{Name: "url", Value: "https://example.com"},
		{Name: "timeout", Value: 30},
	}
	result = lf.Get("http", "request", args2)
	require.Nil(t, result, "Should not find entry with different argument order")

	// Different number of args - should NOT find it
	args3 := []FunctionArg{
		{Name: "url", Value: "https://example.com"},
		{Name: "method", Value: "GET"},
	}
	result = lf.Get("http", "request", args3)
	require.Nil(t, result, "Should not find entry with different number of arguments")
}

func TestComplexTypes(t *testing.T) {
	lf := New()

	// Test that maps/objects are rejected as non-deterministic
	args := []FunctionArg{
		{Name: "config", Value: map[string]interface{}{
			"image": "node:18",
			"env": map[string]string{
				"NODE_ENV": "production",
				"PORT":     "3000",
			},
			"ports": []int{3000, 8080},
		}},
	}

	complexResult := map[string]interface{}{
		"id":     "container-123",
		"status": "running",
		"network": map[string]string{
			"ip":   "172.17.0.2",
			"port": "3000",
		},
	}

	err := lf.Set("docker", "run", args, complexResult)
	require.Error(t, err, "Should error on map in arguments")
	require.Contains(t, err.Error(), "maps/objects cannot be JSON-encoded deterministically")

	// Test with array/slice arguments (should work)
	arrayArgs := []FunctionArg{
		{Name: "ports", Value: []int{3000, 8080}},
		{Name: "tags", Value: []string{"latest", "v1.0"}},
	}

	err = lf.Set("docker", "expose", arrayArgs, "success")
	require.NoError(t, err, "Arrays should be allowed")

	result := lf.Get("docker", "expose", arrayArgs)
	require.Equal(t, "success", result, "Should retrieve array-based entry")
}

func TestDoubleEncoding(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf := New()

	// If someone passes json.RawMessage, it will be double-encoded
	// This test verifies that behavior
	rawJSON := json.RawMessage(`{"complex": {"nested": {"value": 123}}, "array": [1, 2, 3]}`)

	args := []FunctionArg{
		{Name: "data", Value: rawJSON},
	}

	// Set with RawMessage - it will be double-encoded
	err := lf.Set("processor", "transform", args, "result")
	require.NoError(t, err, "RawMessage should be allowed as it's just bytes")

	// Save and load
	require.NoError(t, lf.Save(lockPath), "Failed to save")

	lf2, err := Load(lockPath)
	require.NoError(t, err, "Failed to load")

	// Get with the same RawMessage value (will be double-encoded for comparison)
	result := lf2.Get("processor", "transform", args)
	require.Equal(t, "result", result, "Should find entry even with double-encoded RawMessage")

	// Maps should be rejected even when nested
	regularArgs := []FunctionArg{
		{Name: "data", Value: map[string]interface{}{
			"complex": map[string]interface{}{
				"nested": map[string]interface{}{
					"value": float64(123),
				},
			},
			"array": []interface{}{float64(1), float64(2), float64(3)},
		}},
	}

	err = lf2.Set("processor", "regular", regularArgs, "regular-result")
	require.Error(t, err, "Should error on nested maps")
	require.Contains(t, err.Error(), "maps/objects cannot be JSON-encoded deterministically")
}

func TestUpdateExisting(t *testing.T) {
	lf := New()

	args := []FunctionArg{
		{Name: "pkg", Value: "github.com/example/pkg"},
	}

	// Set initial value
	err := lf.Set("go", "module", args, "v1.0.0")
	require.NoError(t, err)
	require.True(t, lf.IsDirty(), "Should be dirty after first Set")

	// Save the dirty state
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")
	lf.Save(lockPath)

	// Set same value again - should not mark as dirty
	err = lf.Set("go", "module", args, "v1.0.0")
	require.NoError(t, err)
	require.False(t, lf.IsDirty(), "Should not be dirty when setting same value")

	// Update with new value
	err = lf.Set("go", "module", args, "v1.0.1")
	require.NoError(t, err)
	require.True(t, lf.IsDirty(), "Should be dirty after updating value")

	result := lf.Get("go", "module", args)
	require.Equal(t, "v1.0.1", result)

	// Verify only one entry exists
	lf.Save(lockPath)
	lf2, err := Load(lockPath)
	require.NoError(t, err)

	// Count entries (access internal state for testing)
	require.Equal(t, 1, len(lf2.entries), "Expected 1 entry")
}

func TestEmptyAndNilValues(t *testing.T) {
	lf := New()

	// Test with nil argument value
	args1 := []FunctionArg{
		{Name: "value", Value: nil},
	}
	err := lf.Set("test", "nil", args1, "handled-nil")
	require.NoError(t, err, "Nil should be allowed")

	result := lf.Get("test", "nil", args1)
	require.Equal(t, "handled-nil", result, "Failed to handle nil argument")

	// Test with empty string
	args2 := []FunctionArg{
		{Name: "value", Value: ""},
	}
	err = lf.Set("test", "empty", args2, "handled-empty")
	require.NoError(t, err, "Empty string should be allowed")

	result = lf.Get("test", "empty", args2)
	require.Equal(t, "handled-empty", result, "Failed to handle empty string")

	// Test with empty array
	args3 := []FunctionArg{
		{Name: "value", Value: []interface{}{}},
	}
	err = lf.Set("test", "emptyarray", args3, "handled-empty-array")
	require.NoError(t, err, "Empty array should be allowed")

	result = lf.Get("test", "emptyarray", args3)
	require.Equal(t, "handled-empty-array", result, "Failed to handle empty array")

	// Test with no arguments
	args4 := []FunctionArg{}
	err = lf.Set("test", "noargs", args4, "no-arguments")
	require.NoError(t, err, "No arguments should be allowed")

	result = lf.Get("test", "noargs", args4)
	require.Equal(t, "no-arguments", result, "Failed to handle no arguments")
}

func TestFileNotExist(t *testing.T) {
	// Load non-existent file
	lf, err := Load("/tmp/definitely-does-not-exist-lockfile.lock")
	require.NoError(t, err, "Load should return empty lockfile for non-existent file")

	require.NotNil(t, lf, "Load should return non-nil lockfile for non-existent file")

	// Should be empty
	result := lf.Get("any", "function", []FunctionArg{{Name: "test", Value: "value"}})
	require.Nil(t, result, "Empty lockfile should return nil for any Get")
}

func TestSaveNoChanges(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf := New()

	// Save without changes - should not create file
	require.NoError(t, lf.Save(lockPath), "Save should succeed even with no changes")

	// File should not exist because nothing was dirty
	require.NoFileExists(t, lockPath)

	// Add entry and save
	err := lf.Set("test", "func", []FunctionArg{{Name: "a", Value: "b"}}, "result")
	require.NoError(t, err)
	require.NoError(t, lf.Save(lockPath))

	// File should exist now
	require.FileExists(t, lockPath, "File should exist after saving dirty lockfile")

	// Save again without changes
	require.NoError(t, lf.Save(lockPath))
}

func TestNonDeterministicTypeValidation(t *testing.T) {
	lf := New()

	t.Run("maps in arguments are rejected", func(t *testing.T) {
		args := []FunctionArg{
			{Name: "config", Value: map[string]interface{}{
				"key": "value",
			}},
		}
		err := lf.Set("test", "func", args, "result")
		require.Error(t, err, "Should reject map in arguments")
		require.Contains(t, err.Error(), "maps/objects cannot be JSON-encoded deterministically")
		require.Contains(t, err.Error(), "argument \"config\"")
	})

	t.Run("maps in results are rejected", func(t *testing.T) {
		args := []FunctionArg{
			{Name: "id", Value: "test-id"},
		}
		result := map[string]string{
			"status": "ok",
		}
		err := lf.Set("test", "func", args, result)
		require.Error(t, err, "Should reject map in result")
		require.Contains(t, err.Error(), "maps/objects cannot be JSON-encoded deterministically")
		require.Contains(t, err.Error(), "result:")
	})

	t.Run("nested maps are rejected", func(t *testing.T) {
		// Map nested in array
		args1 := []FunctionArg{
			{Name: "list", Value: []interface{}{
				"string-ok",
				123,
				map[string]string{"nested": "map"},
			}},
		}
		err := lf.Set("test", "func", args1, "result")
		require.Error(t, err, "Should reject map nested in array")
		require.Contains(t, err.Error(), "maps/objects cannot be JSON-encoded deterministically")

		// Map nested in struct (if using structs)
		type testStruct struct {
			Name string
			Data map[string]interface{}
		}
		args2 := []FunctionArg{
			{Name: "struct", Value: testStruct{
				Name: "test",
				Data: map[string]interface{}{"key": "value"},
			}},
		}
		err = lf.Set("test", "func", args2, "result")
		require.Error(t, err, "Should reject map nested in struct")
		require.Contains(t, err.Error(), "maps/objects cannot be JSON-encoded deterministically")
	})

	t.Run("deterministic types are allowed", func(t *testing.T) {
		// Primitives
		err := lf.Set("test", "primitives", []FunctionArg{
			{Name: "string", Value: "hello"},
			{Name: "int", Value: 42},
			{Name: "float", Value: 3.14},
			{Name: "bool", Value: true},
			{Name: "nil", Value: nil},
		}, "primitive-result")
		require.NoError(t, err, "Primitives should be allowed")

		// Arrays/slices
		err = lf.Set("test", "arrays", []FunctionArg{
			{Name: "strings", Value: []string{"a", "b", "c"}},
			{Name: "numbers", Value: []int{1, 2, 3}},
			{Name: "nested", Value: [][]int{{1, 2}, {3, 4}}},
		}, []string{"result", "array"})
		require.NoError(t, err, "Arrays should be allowed")

		// Structs without maps
		type safeStruct struct {
			Name   string
			Count  int
			Values []string
		}
		err = lf.Set("test", "structs", []FunctionArg{
			{Name: "data", Value: safeStruct{
				Name:   "test",
				Count:  5,
				Values: []string{"a", "b"},
			}},
		}, safeStruct{Name: "result", Count: 10, Values: nil})
		require.NoError(t, err, "Structs without maps should be allowed")

		// json.RawMessage (just bytes)
		raw := json.RawMessage(`{"key": "value"}`)
		err = lf.Set("test", "raw", []FunctionArg{
			{Name: "raw", Value: raw},
		}, "raw-result")
		require.NoError(t, err, "json.RawMessage should be allowed as it's treated as bytes")
	})

	t.Run("Get skips entries with non-deterministic arguments", func(t *testing.T) {
		// This tests that Get returns nil when given non-deterministic args
		args := []FunctionArg{
			{Name: "config", Value: map[string]interface{}{
				"key": "value",
			}},
		}

		result := lf.Get("test", "func", args)
		require.Nil(t, result, "Get should return nil for non-deterministic arguments")
	})
}
