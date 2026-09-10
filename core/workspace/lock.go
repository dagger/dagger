package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/dagger/dagger/util/lockfile"
)

const (
	LockDirName = ".dagger"

	LockFileName       = "dagger.lock"
	LegacyLockFileName = "lock"
	LegacyLockFilePath = LockDirName + "/" + LegacyLockFileName

	CoreLockNamespace      = ""
	LockOperationOCILatest = "oci-latest"
	LockOperationOCISHA    = "oci-sha"
	LockOperationGitLatest = "git-latest"
	LockOperationGitSHA    = "git-sha"
	LockOperationVanityURL = "vanity-url"

	LatestReleaseVersion  = "v1.0.0-beta.11"
	VersionQueriesVersion = "v1.0.0-beta.12"
)

// ErrGitLockIdentityConflict indicates that transport-specific entries for the
// same Git repository contain different values and cannot be normalized safely.
var ErrGitLockIdentityConflict = errors.New("conflicting Git lock identities")

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

// LookupEntry is a structured lockfile lookup tuple.
type LookupEntry struct {
	Namespace string
	Operation string
	Inputs    []any
	Value     string
}

// LookupOption is an optional input to a lock operation. Options are encoded
// as ordered key-value pairs after the required positional inputs.
type LookupOption struct {
	Name  string
	Value any
}

// LookupInputs combines required positional inputs with optional named inputs.
func LookupInputs(required []any, options ...LookupOption) []any {
	inputs := append([]any(nil), required...)
	if len(options) == 0 {
		return inputs
	}
	pairs := make([]any, 0, len(options))
	for _, option := range options {
		pairs = append(pairs, []any{option.Name, option.Value})
	}
	return append(inputs, pairs...)
}

// ParseLookupInputs separates required positional inputs from optional named
// inputs.
func ParseLookupInputs(inputs []any) ([]any, map[string]any, error) {
	required := inputs
	options := map[string]any{}
	if len(inputs) == 0 {
		return required, options, nil
	}
	optionStart := len(inputs)
	for optionStart > 0 {
		pair, ok := inputs[optionStart-1].([]any)
		if !ok || len(pair) != 2 {
			break
		}
		name, ok := pair[0].(string)
		if !ok || name == "" {
			break
		}
		optionStart--
	}
	for _, rawPair := range inputs[optionStart:] {
		pair, ok := rawPair.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("invalid lock option")
		}
		name := pair[0].(string)
		if _, exists := options[name]; exists {
			return nil, nil, fmt.Errorf("duplicate lock option %q", name)
		}
		options[name] = pair[1]
	}
	return inputs[:optionStart], options, nil
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

	// Git transports are local access details, not part of a repository's
	// identity. Rebuild parsed locks so legacy transport-qualified entries remain
	// readable and the next lockfile write uses the canonical scheme-less key.
	normalized := lockfile.New()
	for _, entry := range file.Entries() {
		inputs := normalizeLookupInputs(entry.Operation, entry.Inputs)
		if existing, ok := normalized.Get(entry.Namespace, entry.Operation, inputs); ok && existing != entry.Value {
			return nil, fmt.Errorf(
				"%w: different values for %s inputs %v: %q and %q",
				ErrGitLockIdentityConflict,
				entry.Operation,
				inputs,
				existing,
				entry.Value,
			)
		}
		if err := normalized.Set(entry.Namespace, entry.Operation, inputs, entry.Value); err != nil {
			return nil, err
		}
	}
	return &Lock{file: normalized}, nil
}

// FutureLockfileVersionError turns an unsupported future lockfile version into
// actionable engine upgrade guidance. It returns nil for all other parse errors.
func FutureLockfileVersionError(err error) error {
	var versionErr *lockfile.UnsupportedVersionError
	if !errors.As(err, &versionErr) || !versionErr.IsNewer() {
		return nil
	}
	return fmt.Errorf(
		"lockfile version %q is newer than supported version %q; upgrade Dagger to continue",
		versionErr.Version,
		versionErr.Supported,
	)
}

// LockfileMergeConflictError turns lockfile conflict markers into actionable
// guidance. It returns nil for all other parse errors.
func LockfileMergeConflictError(err error) error {
	if errors.Is(err, lockfile.ErrMergeConflict) {
		return fmt.Errorf(
			"workspace lockfile contains merge conflict markers; resolve the conflict before running Dagger",
		)
	}
	if errors.Is(err, ErrGitLockIdentityConflict) {
		return fmt.Errorf("workspace lockfile contains conflicting pins for the same Git repository; resolve the conflict before running Dagger: %w", err)
	}
	return nil
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
	entries := other.Entries()
	for _, entry := range entries {
		if err := l.setLookup(entry.Namespace, entry.Operation, entry.Inputs, entry.Value); err != nil {
			return err
		}
	}
	return nil
}

// GetLookup retrieves the value for a generic lookup tuple.
func (l *Lock) GetLookup(namespace, operation string, inputs []any) (string, bool) {
	if l == nil {
		return "", false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return "", false
	}
	value, ok := l.file.Get(namespace, operation, normalizeLookupInputs(operation, inputs))
	if !ok {
		return "", false
	}
	return value, true
}

// SetLookup sets the value for a generic lookup tuple.
func (l *Lock) SetLookup(namespace, operation string, inputs []any, value string) error {
	return l.setLookup(namespace, operation, inputs, value)
}

func (l *Lock) setLookup(namespace, operation string, inputs []any, value string) error {
	if l == nil {
		return fmt.Errorf("nil lock")
	}
	if value == "" {
		return fmt.Errorf("lookup value is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return fmt.Errorf("nil lock")
	}
	return l.file.Set(namespace, operation, normalizeLookupInputs(operation, inputs), value)
}

func normalizeLookupInputs(operation string, inputs []any) []any {
	if operation != LockOperationGitLatest && operation != LockOperationGitSHA {
		return inputs
	}
	if len(inputs) == 0 {
		return inputs
	}
	remote, ok := inputs[0].(string)
	if !ok {
		return inputs
	}
	remote = NormalizeGitRemote(remote)
	if remote == "" {
		return inputs
	}
	normalized := append([]any(nil), inputs...)
	normalized[0] = remote
	return normalized
}

// Entries returns a deterministic snapshot of all lookup entries.
func (l *Lock) Entries() []LookupEntry {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.file == nil {
		return nil
	}

	rawEntries := l.file.Entries()
	entries := make([]LookupEntry, 0, len(rawEntries))
	for _, entry := range rawEntries {
		entries = append(entries, LookupEntry{
			Namespace: entry.Namespace,
			Operation: entry.Operation,
			Inputs:    entry.Inputs,
			Value:     entry.Value,
		})
	}
	return entries
}
