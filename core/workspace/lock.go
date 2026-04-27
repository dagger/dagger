package workspace

import (
	"fmt"

	"github.com/dagger/dagger/util/lockfile"
)

const (
	LockDirName  = ".dagger"
	LockFileName = "lock"

	lockModulesResolveOperation = "modules.resolve"
)

// LockMode controls where lookup results come from for a run.
type LockMode string

const (
	LockModeDisabled LockMode = "disabled"
	LockModeLive     LockMode = "live"
	LockModePinned   LockMode = "pinned"
	LockModeFrozen   LockMode = "frozen"

	// Backward-compatible aliases for the previous experimental names.
	LockModeAuto   = LockModePinned
	LockModeStrict = LockModeFrozen

	// DefaultLockMode is used when no mode is explicitly set.
	DefaultLockMode = LockModeDisabled
)

// LockPolicy controls update intent for a lock entry.
type LockPolicy string

const (
	PolicyPin   LockPolicy = "pin"
	PolicyFloat LockPolicy = "float"
)

// LookupResult is the stored lock result for a lookup tuple.
type LookupResult struct {
	Value  string     `json:"value"`
	Policy LockPolicy `json:"policy"`
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
	file *lockfile.Lockfile
}

// ParseLock parses .dagger/lock data.
func ParseLock(data []byte) (*Lock, error) {
	file, err := lockfile.Parse(data)
	if err != nil {
		return nil, err
	}
	return &Lock{file: file}, nil
}

// NewLock returns an empty workspace lock.
func NewLock() *Lock {
	return &Lock{file: lockfile.New()}
}

// Marshal serializes lock entries.
func (l *Lock) Marshal() ([]byte, error) {
	if l == nil || l.file == nil {
		return nil, fmt.Errorf("nil lock")
	}
	return l.file.Marshal()
}

// Clone returns a deep copy of the lock.
func (l *Lock) Clone() (*Lock, error) {
	cloned := NewLock()
	if l == nil || l.file == nil {
		return cloned, nil
	}
	if err := cloned.Merge(l); err != nil {
		return nil, err
	}
	return cloned, nil
}

// Merge applies all entries from other onto l.
func (l *Lock) Merge(other *Lock) error {
	if l == nil || l.file == nil {
		return fmt.Errorf("nil lock")
	}
	if other == nil || other.file == nil {
		return nil
	}
	entries, err := other.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := l.SetLookup(entry.Namespace, entry.Operation, entry.Inputs, entry.Result); err != nil {
			return err
		}
	}
	return nil
}

// GetModuleResolve retrieves the lock result for a module source lookup.
func (l *Lock) GetModuleResolve(source string) (LookupResult, bool, error) {
	return l.GetLookup("", lockModulesResolveOperation, moduleResolveInputs(source))
}

// SetModuleResolve sets the lock result for a module source lookup.
func (l *Lock) SetModuleResolve(source string, result LookupResult) error {
	if source == "" {
		return fmt.Errorf("module source is required")
	}
	return l.SetLookup("", lockModulesResolveOperation, moduleResolveInputs(source), result)
}

// GetLookup retrieves the lock result for a generic lookup tuple.
func (l *Lock) GetLookup(namespace, operation string, inputs []any) (LookupResult, bool, error) {
	if l == nil || l.file == nil {
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
	if l == nil || l.file == nil {
		return fmt.Errorf("nil lock")
	}
	if result.Value == "" {
		return fmt.Errorf("lookup value is required")
	}
	if !isValidLockPolicy(result.Policy) {
		return fmt.Errorf("invalid lock policy %q", result.Policy)
	}
	return l.file.Set(namespace, operation, inputs, result.Value, string(result.Policy))
}

// DeleteLookup removes a generic lookup tuple entry.
func (l *Lock) DeleteLookup(namespace, operation string, inputs []any) bool {
	if l == nil || l.file == nil {
		return false
	}
	return l.file.Delete(namespace, operation, inputs)
}

// Entries returns a deterministic snapshot of all lookup entries.
func (l *Lock) Entries() ([]LookupEntry, error) {
	if l == nil || l.file == nil {
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
	resultValue, ok := value.(string)
	if !ok || resultValue == "" {
		return LookupResult{}, fmt.Errorf("value is required")
	}
	result := LookupResult{
		Value:  resultValue,
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

func moduleResolveInputs(source string) []any {
	return []any{source}
}

// ParseLockMode validates an explicitly configured lock mode.
func ParseLockMode(mode string) (LockMode, error) {
	switch mode {
	case "update":
		return LockModeLive, nil
	case "auto":
		return LockModePinned, nil
	case "strict":
		return LockModeFrozen, nil
	}

	lockMode := LockMode(mode)
	if !isValidLockMode(lockMode) {
		return "", fmt.Errorf("invalid lock mode %q", mode)
	}
	return lockMode, nil
}

// ResolveLockMode applies the branch default when the mode is unspecified.
func ResolveLockMode(mode string) (LockMode, error) {
	if mode == "" {
		return DefaultLockMode, nil
	}
	return ParseLockMode(mode)
}

func isValidLockMode(mode LockMode) bool {
	return mode == LockModeDisabled || mode == LockModeLive || mode == LockModePinned || mode == LockModeFrozen
}
