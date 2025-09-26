package lockfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagger/dagger/util/lockfile"
	"google.golang.org/grpc"
)

var (
	// globalLockfile is the singleton lockfile instance
	globalLockfile *LockfileAttachable
	globalMutex    sync.Mutex
)

// LockfileAttachable implements the lockfile service as a session attachable
type LockfileAttachable struct {
	mu       sync.RWMutex
	lockfile *lockfile.Lockfile
	path     string

	UnimplementedLockfileServer
}

// NewLockfileAttachable creates a new lockfile attachable
func NewLockfileAttachable(workdir string) (*LockfileAttachable, error) {
	l := &LockfileAttachable{}

	// Find lockfile location
	l.path = l.findLockfilePath(workdir)

	// Load existing lockfile if it exists
	lf, err := lockfile.Load(l.path)
	if err != nil {
		// Don't fail on load errors, just log them
		// The lockfile is optional and we can work without it
		fmt.Fprintf(os.Stderr, "Warning: failed to load lockfile: %v\n", err)
		lf = lockfile.New()
	}
	l.lockfile = lf

	// Store as global singleton
	globalMutex.Lock()
	globalLockfile = l
	globalMutex.Unlock()

	return l, nil
}

// findLockfilePath finds the appropriate location for dagger.lock
func (l *LockfileAttachable) findLockfilePath(workdir string) string {
	// Look for dagger.json in workdir or parent directories
	dir := workdir
	for {
		daggerJSONPath := filepath.Join(dir, "dagger.json")
		if _, err := os.Stat(daggerJSONPath); err == nil {
			// Found dagger.json, put lockfile next to it
			return filepath.Join(dir, "dagger.lock")
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root, use workdir
			break
		}
		dir = parent
	}

	// No dagger.json found, use workdir
	return filepath.Join(workdir, "dagger.lock")
}

// Register registers the lockfile service with the gRPC server
func (l *LockfileAttachable) Register(srv *grpc.Server) {
	RegisterLockfileServer(srv, l)
}

// Get retrieves a cached lookup result
func (l *LockfileAttachable) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Convert JSON-encoded args to values
	args := make([]any, len(req.Args))
	for i, argJSON := range req.Args {
		var value any
		if err := json.Unmarshal([]byte(argJSON), &value); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arg %d: %w", i, err)
		}
		args[i] = value
	}

	// Look up in the lockfile
	result := l.lockfile.Get(req.Module, req.Function, args)
	if result == nil {
		// Cache miss
		return &GetResponse{Result: ""}, nil
	}

	// Marshal the result to JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &GetResponse{Result: string(resultJSON)}, nil
}

// Set stores a new lookup result
func (l *LockfileAttachable) Set(ctx context.Context, req *SetRequest) (*SetResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Convert JSON-encoded args to values
	args := make([]any, len(req.Args))
	for i, argJSON := range req.Args {
		var value any
		if err := json.Unmarshal([]byte(argJSON), &value); err != nil {
			return nil, fmt.Errorf("failed to unmarshal arg %d: %w", i, err)
		}
		args[i] = value
	}

	// Unmarshal the result
	var result any
	if err := json.Unmarshal([]byte(req.Result), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	// Store in the lockfile
	if err := l.lockfile.Set(req.Module, req.Function, args, result); err != nil {
		return nil, fmt.Errorf("failed to set lockfile entry: %w", err)
	}

	return &SetResponse{}, nil
}

// Save writes the lockfile to disk if there are changes
func (l *LockfileAttachable) Save() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.lockfile.Save(l.path)
}

// Close saves any pending changes
func (l *LockfileAttachable) Close() error {
	return l.Save()
}

// GetGlobal retrieves a value from the global lockfile instance (if available)
// This is a helper for core code to access the lockfile without going through RPC
func GetGlobal(module, function string, args []any) any {
	globalMutex.Lock()
	lockfileAttachable := globalLockfile
	globalMutex.Unlock()

	if lockfileAttachable == nil {
		return nil
	}

	lockfileAttachable.mu.RLock()
	defer lockfileAttachable.mu.RUnlock()

	return lockfileAttachable.lockfile.Get(module, function, args)
}

// SetGlobal stores a value in the global lockfile instance (if available)
// This is a helper for core code to access the lockfile without going through RPC
func SetGlobal(module, function string, args []any, result any) error {
	globalMutex.Lock()
	lockfileAttachable := globalLockfile
	globalMutex.Unlock()

	if lockfileAttachable == nil {
		return fmt.Errorf("lockfile not initialized")
	}

	lockfileAttachable.mu.Lock()
	defer lockfileAttachable.mu.Unlock()

	return lockfileAttachable.lockfile.Set(module, function, args, result)
}
