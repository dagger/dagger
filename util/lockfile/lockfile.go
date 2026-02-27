package lockfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	versionKey   = "version"
	versionValue = "1"
)

// Lockfile stores lock entries keyed by (namespace, operation, inputs).
type Lockfile struct {
	entries map[tupleKey]lockEntry
}

type tupleKey struct {
	namespace  string
	operation  string
	inputsJSON string
}

type lockEntry struct {
	namespace  string
	operation  string
	inputs     []any
	inputsJSON string
	result     any
}

// Entry is a single lockfile tuple entry.
type Entry struct {
	Namespace string
	Operation string
	Inputs    []any
	Result    any
}

// New returns an empty lockfile.
func New() *Lockfile {
	return &Lockfile{entries: map[tupleKey]lockEntry{}}
}

// Parse decodes lockfile JSON lines.
//
// Non-empty files must start with a version header line: [["version","1"]].
func Parse(data []byte) (*Lockfile, error) {
	lock := New()
	lines := bytes.Split(data, []byte("\n"))

	firstContentLine := true
	for i, rawLine := range lines {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}

		if firstContentLine {
			if err := parseVersionHeader([]byte(line)); err != nil {
				return nil, fmt.Errorf("lockfile line %d: %w", i+1, err)
			}
			firstContentLine = false
			continue
		}

		entry, err := parseEntry([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("lockfile line %d: %w", i+1, err)
		}
		lock.entries[entryKey(entry.namespace, entry.operation, entry.inputsJSON)] = entry
	}

	return lock, nil
}

// Marshal encodes lockfile entries to deterministic JSON lines.
//
// Empty lockfiles marshal to empty bytes.
func (l *Lockfile) Marshal() ([]byte, error) {
	if len(l.entries) == 0 {
		return nil, nil
	}

	lines := make([][]byte, 0, len(l.entries)+1)
	header, err := json.Marshal([][]string{{versionKey, versionValue}})
	if err != nil {
		return nil, fmt.Errorf("marshal lockfile header: %w", err)
	}
	lines = append(lines, header)

	for _, entry := range l.sortedEntries() {
		line, err := json.Marshal([]any{entry.namespace, entry.operation, entry.inputs, entry.result})
		if err != nil {
			return nil, fmt.Errorf("marshal lockfile entry %q %q: %w", entry.namespace, entry.operation, err)
		}
		lines = append(lines, line)
	}

	return bytes.Join(lines, []byte("\n")), nil
}

// Get retrieves the result for (namespace, operation, inputs).
func (l *Lockfile) Get(namespace, operation string, inputs []any) (any, bool) {
	if l == nil || len(l.entries) == 0 {
		return nil, false
	}
	_, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return nil, false
	}
	entry, ok := l.entries[entryKey(namespace, operation, inputsJSON)]
	if !ok {
		return nil, false
	}
	result, err := canonicalizeAny(entry.result)
	if err != nil {
		return nil, false
	}
	return result, true
}

// Set records the result for (namespace, operation, inputs).
func (l *Lockfile) Set(namespace, operation string, inputs []any, result any) error {
	if l == nil {
		return fmt.Errorf("nil lockfile")
	}
	if l.entries == nil {
		l.entries = map[tupleKey]lockEntry{}
	}

	canonicalInputs, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return fmt.Errorf("canonicalizing lock inputs: %w", err)
	}
	canonicalResult, err := canonicalizeAny(result)
	if err != nil {
		return fmt.Errorf("canonicalizing lock result: %w", err)
	}

	l.entries[entryKey(namespace, operation, inputsJSON)] = lockEntry{
		namespace:  namespace,
		operation:  operation,
		inputs:     canonicalInputs,
		inputsJSON: inputsJSON,
		result:     canonicalResult,
	}
	return nil
}

// Delete removes the result for (namespace, operation, inputs).
func (l *Lockfile) Delete(namespace, operation string, inputs []any) bool {
	if l == nil || len(l.entries) == 0 {
		return false
	}
	_, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return false
	}
	key := entryKey(namespace, operation, inputsJSON)
	if _, ok := l.entries[key]; !ok {
		return false
	}
	delete(l.entries, key)
	return true
}

// Entries returns a deterministic snapshot of all lock entries.
func (l *Lockfile) Entries() []Entry {
	if l == nil || len(l.entries) == 0 {
		return nil
	}

	entries := make([]Entry, 0, len(l.entries))
	for _, entry := range l.sortedEntries() {
		inputs, _, err := canonicalizeInputs(entry.inputs)
		if err != nil {
			continue
		}
		result, err := canonicalizeAny(entry.result)
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Namespace: entry.namespace,
			Operation: entry.operation,
			Inputs:    inputs,
			Result:    result,
		})
	}
	return entries
}

func parseVersionHeader(line []byte) error {
	var header []json.RawMessage
	if err := decodeJSON(line, &header); err != nil {
		return fmt.Errorf("invalid version header: %w", err)
	}
	if len(header) != 1 {
		return fmt.Errorf("missing version header")
	}
	var versionPair []string
	if err := decodeJSON(header[0], &versionPair); err != nil {
		return fmt.Errorf("missing version header")
	}
	if len(versionPair) != 2 || versionPair[0] != versionKey {
		return fmt.Errorf("missing version header")
	}
	if versionPair[1] != versionValue {
		return fmt.Errorf("unsupported lockfile version %q", versionPair[1])
	}
	return nil
}

func parseEntry(line []byte) (lockEntry, error) {
	var tuple []json.RawMessage
	if err := decodeJSON(line, &tuple); err != nil {
		return lockEntry{}, fmt.Errorf("invalid tuple JSON: %w", err)
	}
	if len(tuple) != 4 {
		return lockEntry{}, fmt.Errorf("invalid tuple length %d: expected 4", len(tuple))
	}

	var namespace string
	if err := decodeJSON(tuple[0], &namespace); err != nil {
		return lockEntry{}, fmt.Errorf("invalid namespace: %w", err)
	}

	var operation string
	if err := decodeJSON(tuple[1], &operation); err != nil {
		return lockEntry{}, fmt.Errorf("invalid operation: %w", err)
	}

	var inputs []any
	if err := decodeJSON(tuple[2], &inputs); err != nil {
		return lockEntry{}, fmt.Errorf("invalid inputs: %w", err)
	}
	canonicalInputs, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return lockEntry{}, fmt.Errorf("canonicalizing inputs: %w", err)
	}

	var result any
	if err := decodeJSON(tuple[3], &result); err != nil {
		return lockEntry{}, fmt.Errorf("invalid result: %w", err)
	}
	canonicalResult, err := canonicalizeAny(result)
	if err != nil {
		return lockEntry{}, fmt.Errorf("canonicalizing result: %w", err)
	}

	return lockEntry{
		namespace:  namespace,
		operation:  operation,
		inputs:     canonicalInputs,
		inputsJSON: inputsJSON,
		result:     canonicalResult,
	}, nil
}

func canonicalizeInputs(inputs []any) ([]any, string, error) {
	if inputs == nil {
		inputs = []any{}
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		return nil, "", err
	}
	var canonical []any
	if err := decodeJSON(data, &canonical); err != nil {
		return nil, "", err
	}
	normalized, err := normalizeOrderedInputs(canonical)
	if err != nil {
		return nil, "", err
	}
	data, err = json.Marshal(normalized)
	if err != nil {
		return nil, "", err
	}
	return normalized, string(data), nil
}

func canonicalizeAny(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var canonical any
	if err := decodeJSON(data, &canonical); err != nil {
		return nil, err
	}
	return canonical, nil
}

func normalizeOrderedInputs(inputs []any) ([]any, error) {
	out := make([]any, len(inputs))
	for i, value := range inputs {
		normalized, err := normalizeOrderedInputValue(value)
		if err != nil {
			return nil, fmt.Errorf("input %d: %w", i, err)
		}
		out[i] = normalized
	}
	return out, nil
}

func normalizeOrderedInputValue(value any) (any, error) {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for i, nested := range typed {
			normalized, err := normalizeOrderedInputValue(nested)
			if err != nil {
				return nil, fmt.Errorf("nested input %d: %w", i, err)
			}
			out[i] = normalized
		}
		return out, nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		pairs := make([]any, 0, len(keys))
		for _, key := range keys {
			normalized, err := normalizeOrderedInputValue(typed[key])
			if err != nil {
				return nil, fmt.Errorf("input key %q: %w", key, err)
			}
			pairs = append(pairs, []any{key, normalized})
		}
		return pairs, nil
	}
	return value, nil
}

func decodeJSON(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing content")
		}
		return err
	}
	return nil
}

func entryKey(namespace, operation, inputsJSON string) tupleKey {
	return tupleKey{
		namespace:  namespace,
		operation:  operation,
		inputsJSON: inputsJSON,
	}
}

func (l *Lockfile) sortedEntries() []lockEntry {
	entries := make([]lockEntry, 0, len(l.entries))
	for _, entry := range l.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].namespace != entries[j].namespace {
			return entries[i].namespace < entries[j].namespace
		}
		if entries[i].operation != entries[j].operation {
			return entries[i].operation < entries[j].operation
		}
		return entries[i].inputsJSON < entries[j].inputsJSON
	})
	return entries
}
