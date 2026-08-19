package workspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagger/dagger/util/lockfile"
)

const (
	LockDirName = ".dagger"

	LockFileName       = "dagger.lock"
	LegacyLockFileName = "lock"
	LegacyLockFilePath = LockDirName + "/" + LegacyLockFileName
)

// CanonicalLockFilePath maps the legacy .dagger/lock path to its dagger.lock
// sibling. Other paths are already canonical.
func CanonicalLockFilePath(lockFile string) string {
	if lockFile == "" {
		return ""
	}
	lockFile = filepath.Clean(lockFile)
	if filepath.Base(lockFile) != LegacyLockFileName {
		return lockFile
	}
	lockDir := filepath.Dir(lockFile)
	if filepath.Base(lockDir) != LockDirName {
		return lockFile
	}
	canonicalDir := filepath.Dir(lockDir)
	if canonicalDir == "." {
		return LockFileName
	}
	return filepath.Join(canonicalDir, LockFileName)
}

// LegacyLockFilePathForCanonical returns the legacy lockfile path that used to
// sit next to a canonical dagger.lock.
func LegacyLockFilePathForCanonical(lockFile string) string {
	lockDir := filepath.Dir(CanonicalLockFilePath(lockFile))
	return filepath.Join(lockDir, LegacyLockFilePath)
}

// LockPolicy controls update intent for a lock entry. It remains in the
// in-memory model only to migrate policies read from version 1 lockfiles.
type LockPolicy string

const (
	PolicyPin   LockPolicy = "pin"
	PolicyFloat LockPolicy = "float"
)

// LookupResult is the stored lock result for a lookup tuple.
type LookupResult struct {
	Value  any        `json:"value"`
	Policy LockPolicy `json:"policy"`
}

// GitRefLockResult is the result of resolving a Git ref selector.
type GitRefLockResult struct {
	SHA string `json:"sha"`
	Ref string `json:"ref,omitempty"`
}

// ParseGitRefLockResult decodes and validates a structured Git ref result.
func ParseGitRefLockResult(value any) (GitRefLockResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return GitRefLockResult{}, fmt.Errorf("marshal git ref lock result: %w", err)
	}
	var result GitRefLockResult
	if err := json.Unmarshal(data, &result); err != nil {
		return GitRefLockResult{}, fmt.Errorf("decode git ref lock result: %w", err)
	}
	if result.SHA == "" {
		return GitRefLockResult{}, fmt.Errorf("git ref lock result SHA is required")
	}
	return result, nil
}

// LookupEntry is a structured lockfile lookup tuple.
type LookupEntry struct {
	Namespace string
	Operation string
	Inputs    []any
	Result    LookupResult
}

// Lock is the workspace lockfile wrapper.
type Lock struct {
	mu   sync.RWMutex
	file *lockfile.Lockfile
}

// ParseLock parses dagger.lock data.
func ParseLock(data []byte) (*Lock, error) {
	file, err := lockfile.Parse(data)
	if err != nil {
		return nil, err
	}
	file, err = migrateLegacyGitEntries(file)
	if err != nil {
		return nil, err
	}
	return &Lock{file: file}, nil
}

func migrateLegacyGitEntries(file *lockfile.Lockfile) (*lockfile.Lockfile, error) {
	migrated := lockfile.New()
	for _, entry := range file.Entries() {
		operation, inputs, value := entry.Operation, entry.Inputs, entry.Value
		var err error
		if entry.Namespace == "" {
			operation, inputs, value, err = migrateLegacyGitEntry(operation, inputs, value)
		}
		if err != nil {
			return nil, fmt.Errorf("migrate %s %v: %w", entry.Operation, entry.Inputs, err)
		}

		if existing, existingPolicy, ok := migrated.Get(entry.Namespace, operation, inputs); ok {
			existingResult, existingErr := ParseGitRefLockResult(existing)
			incomingResult, incomingErr := ParseGitRefLockResult(value)
			if existingErr != nil || incomingErr != nil || existingResult != incomingResult {
				return nil, fmt.Errorf("conflicting lock entries for %s %v", operation, inputs)
			}
			if existingPolicy == string(PolicyFloat) || entry.Policy == "" {
				continue
			}
		}

		if entry.Policy != "" {
			err = migrated.SetWithLegacyPolicy(entry.Namespace, operation, inputs, value, entry.Policy)
		} else {
			err = migrated.Set(entry.Namespace, operation, inputs, value)
		}
		if err != nil {
			return nil, err
		}
	}
	return migrated, nil
}

func migrateLegacyGitEntry(operation string, inputs []any, value any) (string, []any, any, error) {
	const gitRefOperation = "git.ref"

	if operation == gitRefOperation {
		if _, ok := value.(string); !ok {
			return operation, inputs, value, nil
		}
	}

	var selector, canonicalRef string
	switch operation {
	case "git.head":
		if len(inputs) != 1 {
			return "", nil, nil, fmt.Errorf("expected one input")
		}
		selector = "HEAD"
	case "git.branch", "git.tag", gitRefOperation:
		if len(inputs) != 2 {
			return "", nil, nil, fmt.Errorf("expected two inputs")
		}
		name, ok := inputs[1].(string)
		if !ok || name == "" {
			return "", nil, nil, fmt.Errorf("ref name is required")
		}
		selector = name
		switch operation {
		case "git.branch":
			selector = "refs/heads/" + strings.TrimPrefix(name, "refs/heads/")
			canonicalRef = selector
		case "git.tag":
			selector = "refs/tags/" + strings.TrimPrefix(name, "refs/tags/")
			canonicalRef = selector
		case gitRefOperation:
			if strings.HasPrefix(name, "refs/") {
				canonicalRef = name
			}
		}
	default:
		return operation, inputs, value, nil
	}

	remote, ok := inputs[0].(string)
	if !ok || remote == "" {
		return "", nil, nil, fmt.Errorf("remote is required")
	}
	sha, ok := value.(string)
	if !ok || sha == "" {
		return "", nil, nil, fmt.Errorf("commit SHA is required")
	}
	return gitRefOperation, []any{remote, selector}, GitRefLockResult{
		SHA: sha,
		Ref: canonicalRef,
	}, nil
}

// NewLock returns an empty workspace lock.
func NewLock() *Lock {
	return &Lock{file: lockfile.New()}
}

// Marshal serializes lock entries.
func (l *Lock) Marshal() ([]byte, error) {
	if l == nil {
		return nil, fmt.Errorf("nil lock")
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return nil, fmt.Errorf("nil lock")
	}
	return l.file.Marshal()
}

// Clone returns a deep copy of the lock.
func (l *Lock) Clone() (*Lock, error) {
	cloned := NewLock()
	if l == nil {
		return cloned, nil
	}
	if err := cloned.Merge(l); err != nil {
		return nil, err
	}
	return cloned, nil
}

// Merge applies all entries from other onto l.
func (l *Lock) Merge(other *Lock) error {
	if l == nil {
		return fmt.Errorf("nil lock")
	}
	l.mu.RLock()
	initialized := l.file != nil
	l.mu.RUnlock()
	if !initialized {
		return fmt.Errorf("nil lock")
	}
	if other == nil {
		return nil
	}
	entries, err := other.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := l.setLookup(entry.Namespace, entry.Operation, entry.Inputs, entry.Result, true); err != nil {
			return err
		}
	}
	return nil
}

// GetLookup retrieves the lock result for a generic lookup tuple.
func (l *Lock) GetLookup(namespace, operation string, inputs []any) (LookupResult, bool, error) {
	if l == nil {
		return LookupResult{}, false, nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return LookupResult{}, false, nil
	}
	value, policy, ok := l.file.Get(namespace, operation, inputs)
	if !ok {
		return LookupResult{}, false, nil
	}
	result, err := parseLookupResult(value, policy)
	if err != nil {
		return LookupResult{}, false, err
	}
	return result, true, nil
}

// SetLookup sets the lock result for a generic lookup tuple.
func (l *Lock) SetLookup(namespace, operation string, inputs []any, result LookupResult) error {
	return l.setLookup(namespace, operation, inputs, result, false)
}

func (l *Lock) setLookup(namespace, operation string, inputs []any, result LookupResult, preserveLegacyPolicy bool) error {
	if l == nil {
		return fmt.Errorf("nil lock")
	}
	if result.Value == nil {
		return fmt.Errorf("lookup value is required")
	}
	if value, ok := result.Value.(string); ok && value == "" {
		return fmt.Errorf("lookup value is required")
	}
	if !isValidLockPolicy(result.Policy) {
		return fmt.Errorf("invalid lock policy %q", result.Policy)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return fmt.Errorf("nil lock")
	}
	if preserveLegacyPolicy {
		return l.file.SetWithLegacyPolicy(namespace, operation, inputs, result.Value, string(result.Policy))
	}
	return l.file.Set(namespace, operation, inputs, result.Value)
}

// DeleteLookup removes a generic lookup tuple entry.
func (l *Lock) DeleteLookup(namespace, operation string, inputs []any) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return false
	}
	return l.file.Delete(namespace, operation, inputs)
}

// Entries returns a deterministic snapshot of all lookup entries.
func (l *Lock) Entries() ([]LookupEntry, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return nil, nil
	}

	rawEntries := l.file.Entries()
	entries := make([]LookupEntry, 0, len(rawEntries))
	for _, entry := range rawEntries {
		result, err := parseLookupResult(entry.Value, entry.Policy)
		if err != nil {
			return nil, err
		}
		entries = append(entries, LookupEntry{
			Namespace: entry.Namespace,
			Operation: entry.Operation,
			Inputs:    entry.Inputs,
			Result:    result,
		})
	}
	return entries, nil
}

func parseLookupResult(value any, policy string) (LookupResult, error) {
	if value == nil {
		return LookupResult{}, fmt.Errorf("value is required")
	}
	if resultValue, ok := value.(string); ok && resultValue == "" {
		return LookupResult{}, fmt.Errorf("value is required")
	}
	if policy == "" {
		policy = string(PolicyPin)
	}
	result := LookupResult{
		Value:  value,
		Policy: LockPolicy(policy),
	}
	if !isValidLockPolicy(result.Policy) {
		return LookupResult{}, fmt.Errorf("invalid policy %q", result.Policy)
	}
	return result, nil
}

func isValidLockPolicy(policy LockPolicy) bool {
	return policy == PolicyPin || policy == PolicyFloat
}
