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
	versionValueV2 = "2"
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
	value      string
}

// Entry is a single lockfile tuple entry.
type Entry struct {
	Namespace string
	Operation string
	Inputs    []any
	Value     string
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
	header, err := json.Marshal([][]string{{versionKey, versionValueV2}})
	if err != nil {
		return nil, fmt.Errorf("marshal lockfile header: %w", err)
	}
	lines = append(lines, header)

	for _, entry := range l.sortedEntries() {
		requiredInputs, options := splitOptions(entry.inputs)
		tuple := []any{
			entry.namespace,
			entry.operation,
			requiredInputs,
			entry.value,
		}
		if len(options) > 0 {
			tuple = append(tuple, options)
		}
		line, err := json.Marshal(tuple)
		if err != nil {
			return nil, fmt.Errorf("marshal lockfile entry %q %q: %w", entry.namespace, entry.operation, err)
		}
		lines = append(lines, line)
	}

	return bytes.Join(lines, []byte("\n")), nil
}

// Get retrieves the value for (namespace, operation, inputs).
func (l *Lockfile) Get(namespace, operation string, inputs []any) (string, bool) {
	if l == nil || len(l.entries) == 0 {
		return "", false
	}
	_, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return "", false
	}
	entry, ok := l.entries[entryKey(namespace, operation, inputsJSON)]
	if !ok {
		return "", false
	}
	return entry.value, true
}

// Set records the value for (namespace, operation, inputs).
func (l *Lockfile) Set(namespace, operation string, inputs []any, value string) error {
	if l == nil {
		return fmt.Errorf("nil lockfile")
	}
	if l.entries == nil {
		l.entries = map[tupleKey]lockEntry{}
	}
	if value == "" {
		return fmt.Errorf("value is required")
	}

	canonicalInputs, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return fmt.Errorf("canonicalizing lock inputs: %w", err)
	}
	l.entries[entryKey(namespace, operation, inputsJSON)] = lockEntry{
		namespace:  namespace,
		operation:  operation,
		inputs:     canonicalInputs,
		inputsJSON: inputsJSON,
		value:      value,
	}
	return nil
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
		entries = append(entries, Entry{
			Namespace: entry.namespace,
			Operation: entry.operation,
			Inputs:    inputs,
			Value:     entry.value,
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
	if version != versionValueV2 {
		return "", fmt.Errorf("unsupported lockfile version %q", version)
	}
	return version, nil
}

func parseEntry(line []byte, version string) (lockEntry, error) {
	var tuple []json.RawMessage
	if err := decodeJSON(line, &tuple); err != nil {
		return lockEntry{}, fmt.Errorf("invalid tuple JSON: %w", err)
	}
	if len(tuple) < 4 || len(tuple) > 5 {
		return lockEntry{}, fmt.Errorf(
			"invalid tuple length %d: expected 4 or 5 for lockfile version %s",
			len(tuple),
			version,
		)
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
	if len(tuple) == 5 {
		var options []any
		if err := decodeJSON(tuple[4], &options); err != nil {
			return lockEntry{}, fmt.Errorf("invalid options: %w", err)
		}
		if err := validateOptions(options); err != nil {
			return lockEntry{}, fmt.Errorf("invalid options: %w", err)
		}
		if len(options) > 0 {
			inputs = append(inputs, options)
		}
	}
	canonicalInputs, inputsJSON, err := canonicalizeInputs(inputs)
	if err != nil {
		return lockEntry{}, fmt.Errorf("canonicalizing inputs: %w", err)
	}

	var value string
	if err := decodeJSON(tuple[3], &value); err != nil {
		return lockEntry{}, fmt.Errorf("invalid value: %w", err)
	}
	if value == "" {
		return lockEntry{}, fmt.Errorf("invalid value: value is required")
	}
	return lockEntry{
		namespace:  namespace,
		operation:  operation,
		inputs:     canonicalInputs,
		inputsJSON: inputsJSON,
		value:      value,
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
	if required, options := splitOptions(canonical); len(options) > 0 {
		sort.Slice(options, func(i, j int) bool {
			return options[i].([]any)[0].(string) < options[j].([]any)[0].(string)
		})
		required = append(required, options)
		canonical = required
	}
	data, err = json.Marshal(canonical)
	if err != nil {
		return nil, "", err
	}
	return canonical, string(data), nil
}

func splitOptions(inputs []any) ([]any, []any) {
	if len(inputs) == 0 || !isOptions(inputs[len(inputs)-1]) {
		return inputs, nil
	}
	return inputs[:len(inputs)-1], inputs[len(inputs)-1].([]any)
}

func isOptions(value any) bool {
	options, ok := value.([]any)
	if !ok || len(options) == 0 {
		return false
	}
	return validateOptions(options) == nil
}

func validateOptions(options []any) error {
	seen := map[string]struct{}{}
	for _, option := range options {
		pair, ok := option.([]any)
		if !ok || len(pair) != 2 {
			return fmt.Errorf("option must be a key-value pair")
		}
		name, ok := pair[0].(string)
		if !ok || name == "" {
			return fmt.Errorf("option name must be a non-empty string")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate option %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
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
