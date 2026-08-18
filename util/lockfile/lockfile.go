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
	versionKey     = "version"
	versionValueV1 = "1"
	versionValueV2 = "2"
	versionCurrent = versionValueV2
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
	value      any
	policy     string
}

// Entry is a single lockfile tuple entry.
type Entry struct {
	Namespace string
	Operation string
	Inputs    []any
	Value     any
	Policy    string
}

// New returns an empty lockfile.
func New() *Lockfile {
	return &Lockfile{entries: map[tupleKey]lockEntry{}}
}

// Parse decodes lockfile JSON lines.
//
// Non-empty files must start with a supported version header line.
func Parse(data []byte) (*Lockfile, error) {
	lock := New()
	lines := bytes.Split(data, []byte("\n"))

	firstContentLine := true
	version := ""
	for i, rawLine := range lines {
		line := strings.TrimSpace(string(rawLine))
		if line == "" {
			continue
		}

		if firstContentLine {
			var err error
			version, err = parseVersionHeader([]byte(line))
			if err != nil {
				return nil, fmt.Errorf("lockfile line %d: %w", i+1, err)
			}
			firstContentLine = false
			continue
		}

		entry, err := parseEntry([]byte(line), version)
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
	header, err := json.Marshal([][]string{{versionKey, versionCurrent}})
	if err != nil {
		return nil, fmt.Errorf("marshal lockfile header: %w", err)
	}
	lines = append(lines, header)

	for _, entry := range l.sortedEntries() {
		line, err := json.Marshal([]any{entry.namespace, entry.operation, entry.inputs, entry.value})
		if err != nil {
			return nil, fmt.Errorf("marshal lockfile entry %q %q: %w", entry.namespace, entry.operation, err)
		}
		lines = append(lines, line)
	}

	return bytes.Join(lines, []byte("\n")), nil
}

// Get retrieves the value and any legacy v1 policy for (namespace, operation,
// inputs). Version 2 entries have no policy.
func (l *Lockfile) Get(namespace, operation string, inputs []any) (any, string, bool) {
	if l == nil || len(l.entries) == 0 {
		return nil, "", false
	}
	_, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return nil, "", false
	}
	entry, ok := l.entries[entryKey(namespace, operation, inputsJSON)]
	if !ok {
		return nil, "", false
	}
	value, err := canonicalizeValue(entry.value)
	if err != nil {
		return nil, "", false
	}
	return value, entry.policy, true
}

// Set records the value for (namespace, operation, inputs).
func (l *Lockfile) Set(namespace, operation string, inputs []any, value any) error {
	return l.set(namespace, operation, inputs, value, "")
}

// SetWithLegacyPolicy records a value while preserving a version 1 policy in
// memory. Marshal still writes version 2 and omits the policy.
func (l *Lockfile) SetWithLegacyPolicy(namespace, operation string, inputs []any, value any, policy string) error {
	return l.set(namespace, operation, inputs, value, policy)
}

func (l *Lockfile) set(namespace, operation string, inputs []any, value any, policy string) error {
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
	canonicalValue, err := canonicalizeValue(value)
	if err != nil {
		return fmt.Errorf("canonicalizing lock value: %w", err)
	}

	l.entries[entryKey(namespace, operation, inputsJSON)] = lockEntry{
		namespace:  namespace,
		operation:  operation,
		inputs:     canonicalInputs,
		inputsJSON: inputsJSON,
		value:      canonicalValue,
		policy:     policy,
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
		value, err := canonicalizeValue(entry.value)
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Namespace: entry.namespace,
			Operation: entry.operation,
			Inputs:    inputs,
			Value:     value,
			Policy:    entry.policy,
		})
	}
	return entries
}

func parseVersionHeader(line []byte) (string, error) {
	var header []json.RawMessage
	if err := decodeJSON(line, &header); err != nil {
		return "", fmt.Errorf("invalid version header: %w", err)
	}
	if len(header) != 1 {
		return "", fmt.Errorf("missing version header")
	}
	var versionPair []string
	if err := decodeJSON(header[0], &versionPair); err != nil {
		return "", fmt.Errorf("missing version header")
	}
	if len(versionPair) != 2 || versionPair[0] != versionKey {
		return "", fmt.Errorf("missing version header")
	}
	version := versionPair[1]
	if version != versionValueV1 && version != versionValueV2 {
		return "", fmt.Errorf("unsupported lockfile version %q", version)
	}
	return version, nil
}

func parseEntry(line []byte, version string) (lockEntry, error) {
	var tuple []json.RawMessage
	if err := decodeJSON(line, &tuple); err != nil {
		return lockEntry{}, fmt.Errorf("invalid tuple JSON: %w", err)
	}
	expectedLen := 4
	if version == versionValueV1 {
		expectedLen = 5
	}
	if len(tuple) != expectedLen {
		return lockEntry{}, fmt.Errorf("invalid tuple length %d: expected %d for lockfile version %s", len(tuple), expectedLen, version)
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

	var value any
	if err := decodeJSON(tuple[3], &value); err != nil {
		return lockEntry{}, fmt.Errorf("invalid value: %w", err)
	}
	canonicalValue, err := canonicalizeValue(value)
	if err != nil {
		return lockEntry{}, fmt.Errorf("canonicalizing value: %w", err)
	}
	var policy string
	if version == versionValueV1 {
		if err := decodeJSON(tuple[4], &policy); err != nil {
			return lockEntry{}, fmt.Errorf("invalid policy: %w", err)
		}
	}

	return lockEntry{
		namespace:  namespace,
		operation:  operation,
		inputs:     canonicalInputs,
		inputsJSON: inputsJSON,
		value:      canonicalValue,
		policy:     policy,
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
	if err := validateOrderedInputs(canonical); err != nil {
		return nil, "", err
	}
	return canonical, string(data), nil
}

func canonicalizeValue(value any) (any, error) {
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

func validateOrderedInputs(inputs []any) error {
	for i, value := range inputs {
		if err := validateNoMaps(value, "lock inputs"); err != nil {
			return fmt.Errorf("input %d: %w", i, err)
		}
	}
	return nil
}

func validateNoMaps(value any, subject string) error {
	switch typed := value.(type) {
	case []any:
		for i, nested := range typed {
			if err := validateNoMaps(nested, subject); err != nil {
				return fmt.Errorf("nested input %d: %w", i, err)
			}
		}
	case map[string]any:
		return fmt.Errorf("unordered object/map/dict in %s", subject)
	}
	return nil
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
