package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestError_Extensions(t *testing.T) {
	tests := []struct {
		name     string
		values   []*ErrorValue
		expected map[string]any
	}{
		{
			name: "simple string value",
			values: []*ErrorValue{
				{
					Name:  "message",
					Value: JSON(`"hello world"`),
				},
			},
			expected: map[string]any{
				"message": "hello world",
			},
		},
		{
			name: "number value",
			values: []*ErrorValue{
				{
					Name:  "code",
					Value: JSON(`42`),
				},
			},
			expected: map[string]any{
				"code": float64(42),
			},
		},
		{
			name: "boolean value",
			values: []*ErrorValue{
				{
					Name:  "enabled",
					Value: JSON(`true`),
				},
			},
			expected: map[string]any{
				"enabled": true,
			},
		},
		{
			name: "null value",
			values: []*ErrorValue{
				{
					Name:  "optional",
					Value: JSON(`null`),
				},
			},
			expected: map[string]any{
				"optional": nil,
			},
		},
		{
			name: "object value",
			values: []*ErrorValue{
				{
					Name:  "details",
					Value: JSON(`{"file": "test.go", "line": 123}`),
				},
			},
			expected: map[string]any{
				"details": map[string]any{
					"file": "test.go",
					"line": float64(123),
				},
			},
		},
		{
			name: "array value",
			values: []*ErrorValue{
				{
					Name:  "items",
					Value: JSON(`["a", "b", "c"]`),
				},
			},
			expected: map[string]any{
				"items": []any{"a", "b", "c"},
			},
		},
		{
			name: "multiple values",
			values: []*ErrorValue{
				{
					Name:  "message",
					Value: JSON(`"error occurred"`),
				},
				{
					Name:  "code",
					Value: JSON(`500`),
				},
				{
					Name:  "details",
					Value: JSON(`{"context": "test"}`),
				},
			},
			expected: map[string]any{
				"message": "error occurred",
				"code":    float64(500),
				"details": map[string]any{
					"context": "test",
				},
			},
		},
		{
			name:     "empty values",
			values:   []*ErrorValue{},
			expected: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &Error{
				Message: "test error",
				Values:  tt.values,
			}

			ext := err.Extensions()
			require.Equal(t, tt.expected, ext)
		})
	}
}

func TestError_Extensions_PreventDoubleEncoding(t *testing.T) {
	// This test specifically ensures that JSON values are properly unmarshaled
	// and not included as raw JSON strings, which would cause double/triple encoding
	originalData := map[string]any{
		"user":    "john_doe",
		"attempt": float64(3),
		"config": map[string]any{
			"timeout": float64(30),
			"retry":   true,
		},
	}

	// Marshal the data to JSON as it would be stored in ErrorValue.Value
	jsonBytes, err := json.Marshal(originalData)
	require.NoError(t, err)

	errorVal := &ErrorValue{
		Name:  "context",
		Value: JSON(jsonBytes),
	}

	error := &Error{
		Message: "authentication failed",
		Values:  []*ErrorValue{errorVal},
	}

	// Get extensions
	ext := error.Extensions()

	// Verify the data was properly unmarshaled and matches the original structure
	require.Equal(t, originalData, ext["context"])

	// Verify it's not a string (which would indicate the JSON wasn't unmarshaled)
	contextVal, ok := ext["context"]
	require.True(t, ok)
	require.IsType(t, map[string]any{}, contextVal)

	// Verify nested values are accessible directly
	contextMap := contextVal.(map[string]any)
	require.Equal(t, "john_doe", contextMap["user"])
	require.Equal(t, float64(3), contextMap["attempt"])
	require.IsType(t, map[string]any{}, contextMap["config"])
}
