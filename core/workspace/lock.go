package workspace

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/util/lockfile"
)

const (
	lockCoreNamespace    = ""
	lockModulesResolveOp = "modules.resolve"
)

// LockMode controls lockfile read/update behavior for a run.
type LockMode string

const (
	LockModeStrict LockMode = "strict"
	LockModeAuto   LockMode = "auto"
	LockModeUpdate LockMode = "update"

	// DefaultLockMode is used when no mode is explicitly set.
	DefaultLockMode = LockModeAuto
)

// LockPolicy controls update intent for a lock entry.
type LockPolicy string

const (
	PolicyPin   LockPolicy = "pin"
	PolicyFloat LockPolicy = "float"

	// DefaultModuleResolvePolicy is used by workspace module lookup when
	// no policy annotation exists in config.toml.
	DefaultModuleResolvePolicy = PolicyFloat
)

// LookupResult is the structured lock result envelope.
type LookupResult struct {
	Value  string     `json:"value"`
	Policy LockPolicy `json:"policy"`
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
	lock := &Lock{file: file}
	if err := lock.validateModuleResolveEntries(); err != nil {
		return nil, err
	}
	return lock, nil
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

// GetModuleResolve retrieves the lock pin and policy for a module source.
func (l *Lock) GetModuleResolve(source string) (pin string, policy LockPolicy, ok bool) {
	result, ok, err := l.GetLookup(lockCoreNamespace, lockModulesResolveOp, moduleResolveInputs(source))
	if err != nil || !ok {
		return "", "", false
	}
	return result.Value, result.Policy, true
}

// SetModuleResolve sets the lock pin and policy for a module source.
func (l *Lock) SetModuleResolve(source, pin string, policy LockPolicy) error {
	if l == nil || l.file == nil {
		return fmt.Errorf("nil lock")
	}
	if source == "" {
		return fmt.Errorf("module source is required")
	}
	if pin == "" {
		return fmt.Errorf("module pin is required")
	}
	if !isValidLockPolicy(policy) {
		return fmt.Errorf("invalid lock policy %q", policy)
	}
	return l.SetLookup(lockCoreNamespace, lockModulesResolveOp, moduleResolveInputs(source), LookupResult{
		Value:  pin,
		Policy: policy,
	})
}

// DeleteModuleResolve removes a module.resolve entry.
func (l *Lock) DeleteModuleResolve(source string) bool {
	return l.DeleteLookup(lockCoreNamespace, lockModulesResolveOp, moduleResolveInputs(source))
}

// GetLookup retrieves the lock result for a generic lookup tuple.
func (l *Lock) GetLookup(namespace, operation string, inputs []any) (LookupResult, bool, error) {
	if l == nil || l.file == nil {
		return LookupResult{}, false, nil
	}
	resultRaw, ok := l.file.Get(namespace, operation, inputs)
	if !ok {
		return LookupResult{}, false, nil
	}
	result, err := parseLookupResult(resultRaw)
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
	return l.file.Set(namespace, operation, inputs, result)
}

// DeleteLookup removes a generic lookup tuple entry.
func (l *Lock) DeleteLookup(namespace, operation string, inputs []any) bool {
	if l == nil || l.file == nil {
		return false
	}
	return l.file.Delete(namespace, operation, inputs)
}

// PruneModuleResolveEntries removes module.resolve entries whose source is absent
// from validSources. It returns the number of deleted entries.
func (l *Lock) PruneModuleResolveEntries(validSources map[string]struct{}) int {
	if l == nil || l.file == nil {
		return 0
	}
	if validSources == nil {
		validSources = map[string]struct{}{}
	}

	deleted := 0
	for _, entry := range l.file.Entries() {
		if entry.Namespace != lockCoreNamespace || entry.Operation != lockModulesResolveOp {
			continue
		}
		source, ok := parseModuleResolveInputs(entry.Inputs)
		if !ok {
			continue
		}
		if _, keep := validSources[source]; keep {
			continue
		}
		if l.DeleteModuleResolve(source) {
			deleted++
		}
	}
	return deleted
}

func (l *Lock) validateModuleResolveEntries() error {
	for _, entry := range l.file.Entries() {
		if entry.Namespace != lockCoreNamespace || entry.Operation != lockModulesResolveOp {
			continue
		}
		source, ok := parseModuleResolveInputs(entry.Inputs)
		if !ok || source == "" {
			return fmt.Errorf("invalid modules.resolve entry inputs")
		}
		if _, err := parseLookupResult(entry.Result); err != nil {
			return fmt.Errorf("invalid modules.resolve entry result: %w", err)
		}
	}
	return nil
}

func parseModuleResolveInputs(inputs []any) (string, bool) {
	if len(inputs) != 1 {
		return "", false
	}

	source, ok := inputs[0].(string)
	if !ok {
		return "", false
	}
	return source, true
}

func moduleResolveInputs(source string) []any {
	return []any{source}
}

func parseLookupResult(value any) (LookupResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return LookupResult{}, err
	}

	var result LookupResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return LookupResult{}, err
	}
	if result.Value == "" {
		return LookupResult{}, fmt.Errorf("value is required")
	}
	if !isValidLockPolicy(result.Policy) {
		return LookupResult{}, fmt.Errorf("invalid policy %q", result.Policy)
	}
	return result, nil
}

func isValidLockPolicy(policy LockPolicy) bool {
	return policy == PolicyPin || policy == PolicyFloat
}

// ParseLockMode parses lock mode, applying the default for empty input.
func ParseLockMode(mode string) (LockMode, error) {
	if mode == "" {
		return DefaultLockMode, nil
	}

	lockMode := LockMode(mode)
	if !isValidLockMode(lockMode) {
		return "", fmt.Errorf("invalid lock mode %q", mode)
	}
	return lockMode, nil
}

func isValidLockMode(mode LockMode) bool {
	return mode == LockModeStrict || mode == LockModeAuto || mode == LockModeUpdate
}
