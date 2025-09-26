// Package lockfile provides a simple interface for managing dagger.lock files
package lockfile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

// FunctionResult represents the cached result of a function call
type FunctionResult any

// Entry represents a single cached function result (internal representation)
type entry struct {
	Module   string
	Function string
	Args     []json.RawMessage // Ordered array of argument values
	Result   json.RawMessage   // Store result as RawMessage
}

// Lockfile manages cached function results in memory
type Lockfile struct {
	entries []entry
	dirty   bool
}

// New creates a new empty Lockfile
func New() *Lockfile {
	return &Lockfile{
		entries: []entry{},
	}
}

// Load reads a lockfile from disk (if it exists)
// Returns an empty Lockfile if the file doesn't exist
func Load(path string) (*Lockfile, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No lockfile yet, return empty
			return New(), nil
		}
		return nil, fmt.Errorf("failed to open lockfile: %w", err)
	}
	defer f.Close()

	lf := New()
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		// Parse tuple: [module, function, [args...], result]
		var tuple []json.RawMessage
		if err := json.Unmarshal(line, &tuple); err != nil {
			return nil, fmt.Errorf("failed to parse line %d: %w", lineNum, err)
		}

		if len(tuple) != 4 {
			return nil, fmt.Errorf("invalid tuple on line %d: expected 4 elements, got %d", lineNum, len(tuple))
		}

		var e entry

		// Parse module
		if err := json.Unmarshal(tuple[0], &e.Module); err != nil {
			return nil, fmt.Errorf("failed to parse module on line %d: %w", lineNum, err)
		}

		// Parse function
		if err := json.Unmarshal(tuple[1], &e.Function); err != nil {
			return nil, fmt.Errorf("failed to parse function on line %d: %w", lineNum, err)
		}

		// Parse args array (keep as RawMessage array)
		var argsArray []json.RawMessage
		if err := json.Unmarshal(tuple[2], &argsArray); err != nil {
			return nil, fmt.Errorf("failed to parse args on line %d: %w", lineNum, err)
		}
		e.Args = argsArray

		// Keep result as RawMessage
		e.Result = tuple[3]

		lf.entries = append(lf.entries, e)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read lockfile: %w", err)
	}

	return lf, nil
}

// Save writes the lockfile to disk (if there are changes)
func (l *Lockfile) Save(path string) error {
	if !l.dirty {
		return nil
	}

	// Write to temp file first, then rename (atomic)
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create lockfile: %w", err)
	}
	defer f.Close()
	defer os.Remove(tmpPath) // Clean up on error

	// Write each entry as a tuple
	for _, e := range l.entries {
		// Create tuple: [module, function, [args...], result]
		tuple := []json.RawMessage{
			mustMarshal(e.Module),
			mustMarshal(e.Function),
			mustMarshal(e.Args),
			e.Result,
		}

		jsonBytes, err := json.Marshal(tuple)
		if err != nil {
			return fmt.Errorf("failed to marshal entry: %w", err)
		}

		if _, err := f.Write(jsonBytes); err != nil {
			return fmt.Errorf("failed to write entry: %w", err)
		}
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write newline: %w", err)
		}
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close lockfile: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename lockfile: %w", err)
	}

	l.dirty = false
	return nil
}

// Get looks up a cached result by walking through all entries
// args is an ordered array of argument values
// Returns nil if not found
func (l *Lockfile) Get(module, function string, args []any) FunctionResult {
	// Validate args don't contain non-deterministic types
	for _, arg := range args {
		if err := validateDeterministic(arg); err != nil {
			// Skip lookup if args contain non-deterministic types
			return nil
		}
	}

	// Convert args to RawMessage for comparison
	argMessages, err := valuesToRawMessages(args)
	if err != nil {
		return nil
	}

	// Linear search through entries
	for _, e := range l.entries {
		if e.Module == module && e.Function == function && argsEqual(e.Args, argMessages) {
			// Unmarshal result back to interface{}
			var result any
			if err := json.Unmarshal(e.Result, &result); err != nil {
				// Failed to unmarshal, skip this entry
				return nil
			}
			return result
		}
	}

	return nil
}

// Set stores a new cached result
// args is an ordered array of argument values
// If an identical entry exists, it's updated
func (l *Lockfile) Set(module, function string, args []any, result FunctionResult) error {
	// Validate args don't contain non-deterministic types
	for i, arg := range args {
		if err := validateDeterministic(arg); err != nil {
			return fmt.Errorf("argument %d: %w", i, err)
		}
	}

	// Validate result doesn't contain non-deterministic types
	if err := validateDeterministic(result); err != nil {
		return fmt.Errorf("result: %w", err)
	}

	// Convert args to RawMessage
	argMessages, err := valuesToRawMessages(args)
	if err != nil {
		return fmt.Errorf("failed to marshal args: %w", err)
	}

	// Convert result to RawMessage
	resultMessage, err := toRawMessage(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	// Look for existing entry
	for i, e := range l.entries {
		if e.Module == module && e.Function == function && argsEqual(e.Args, argMessages) {
			// Update existing entry if result changed
			if !bytes.Equal(e.Result, resultMessage) {
				l.entries[i].Result = resultMessage
				l.dirty = true
			}
			return nil
		}
	}

	// Add new entry
	l.entries = append(l.entries, entry{
		Module:   module,
		Function: function,
		Args:     argMessages,
		Result:   resultMessage,
	})
	l.dirty = true
	return nil
}

// IsDirty returns true if there are unsaved changes
func (l *Lockfile) IsDirty() bool {
	return l.dirty
}

// valuesToRawMessages converts an array of values to RawMessage array
func valuesToRawMessages(values []any) ([]json.RawMessage, error) {
	messages := make([]json.RawMessage, len(values))
	for i, val := range values {
		msg, err := toRawMessage(val)
		if err != nil {
			return nil, err
		}
		messages[i] = msg
	}
	return messages, nil
}

// toRawMessage converts a value to json.RawMessage
func toRawMessage(v any) (json.RawMessage, error) {
	return json.Marshal(v)
}

// argsEqual compares two RawMessage arrays for equality
func argsEqual(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// mustMarshal marshals a value or panics (for simple types that can't fail)
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// validateDeterministic checks if a value can be JSON-encoded deterministically.
// Returns an error if the value contains maps (objects) which have non-deterministic
// JSON encoding due to unpredictable key ordering.
func validateDeterministic(v any) error {
	if v == nil {
		return nil
	}

	return checkValue(reflect.ValueOf(v))
}

// checkValue recursively checks a reflect.Value for non-deterministic types
func checkValue(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}

	switch v.Kind() {
	case reflect.Map:
		// Maps have non-deterministic iteration order in Go
		return fmt.Errorf("maps/objects cannot be JSON-encoded deterministically")

	case reflect.Slice, reflect.Array:
		// Check each element
		for i := 0; i < v.Len(); i++ {
			if err := checkValue(v.Index(i)); err != nil {
				return err
			}
		}

	case reflect.Struct:
		// Check each field
		for i := 0; i < v.NumField(); i++ {
			if err := checkValue(v.Field(i)); err != nil {
				return err
			}
		}

	case reflect.Ptr, reflect.Interface:
		// Dereference and check
		if !v.IsNil() {
			return checkValue(v.Elem())
		}
	}

	// Primitive types (string, int, float, bool, etc.) are deterministic
	return nil
}
