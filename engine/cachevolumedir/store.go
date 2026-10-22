// Package cachevolumedir keeps cache volumes as plain directories under one
// directory, apart from the engine's snapshots, so that the directory can be
// handed from one engine to the next.
//
// One engine owns the directory at a time. Every volume is named by a digest of
// its identity and carries its own metadata, so an engine that receives the
// directory from another can use it with no database.
package cachevolumedir

import (
	"context"
	"errors"
	"time"
)

// FormatVersion is the on-disk format this package writes, as v<N>/ under the
// directory.
const FormatVersion = 1

// ErrOwned is returned by Open when another process owns the directory.
var ErrOwned = errors.New("cache volume directory is owned by another process")

var errNotImplemented = errors.New("cachevolumedir: not implemented")

// Identity is what names a cache volume. Two calls with equal identities use
// the same directory, in any engine.
type Identity struct {
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key"`
	Sharing   string `json:"sharing,omitempty"`
	Owner     string `json:"owner,omitempty"`
	// Source is the recipe digest of the volume's source directory, or empty.
	Source   string `json:"source,omitempty"`
	Selector string `json:"selector,omitempty"`
}

// ID is the volume's directory name: the hex SHA-256 of the identity's
// canonical JSON, versioned.
func (id Identity) ID() string {
	return ""
}

// Options configures a Store.
type Options struct {
	// EngineVersion is recorded in the metadata of volumes this store creates.
	EngineVersion string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Store is an owned cache volume directory.
type Store struct {
	// beforeRemove, when set, runs before each background removal; tests
	// use it to hold removals.
	beforeRemove func()
}

// Open takes ownership of dir, creating it and its layout if needed, and
// removes what a previous owner left in tmp/ and trash/. It returns ErrOwned
// if another process owns dir.
func Open(dir string, opts Options) (*Store, error) {
	return nil, errNotImplemented
}

// Close gives up ownership. Pins still held lose their protection.
func (s *Store) Close() error {
	return errNotImplemented
}

// Pin keeps a volume from being collected until it is released.
type Pin struct{}

// Path is the volume's contents directory, to bind-mount.
func (p *Pin) Path() string {
	return ""
}

// Release ends the pin. Only the first call counts.
func (p *Pin) Release() {}

// Acquire pins the volume, creating it first if it doesn't exist: populate
// fills the new volume's contents directory, which exists and is empty when it
// is called. Creation of one volume is serialized; a failed populate leaves no
// volume behind. A writable pin marks the volume's stored size stale until a
// measurement taken with no writer finds it again.
func (s *Store) Acquire(ctx context.Context, ident Identity, writable bool, populate func(ctx context.Context, fsDir string) error) (*Pin, error) {
	return nil, errNotImplemented
}

// Entry describes one volume in the directory.
type Entry struct {
	ID       string
	Identity Identity
	// Format is the volume's on-disk format; volumes of another format have
	// no identity.
	Format     int
	SizeBytes  int64
	CreatedAt  time.Time
	LastUsedAt time.Time
	Pinned     bool
}

// Usage lists the volumes, measuring those whose stored size is stale, and
// returns the bytes the whole directory uses, including what is still being
// created or removed.
func (s *Store) Usage(ctx context.Context) ([]Entry, int64, error) {
	return nil, 0, errNotImplemented
}

// Policy decides what Collect removes. Byte values are resolved; zero means
// unset. Thresholds work as in the engine's other cache policies.
type Policy struct {
	ReservedSpace int64
	MaxUsedSpace  int64
	MinFreeSpace  int64
	TargetSpace   int64
	// CurrentFreeSpace is the filesystem's free bytes, for MinFreeSpace.
	CurrentFreeSpace int64
	// KeepDuration keeps volumes used more recently than this.
	KeepDuration time.Duration
	// All removes every volume that isn't pinned, whatever the thresholds
	// and KeepDuration.
	All bool
}

// Report is what one Collect removed.
type Report struct {
	Removed        []Entry
	ReclaimedBytes int64
}

// Collect removes volumes until the policy is met: volumes of another format
// first, then the least recently used, never a pinned one. Bytes already being
// removed count as freed. Removed volumes are moved aside at once and deleted
// in the background. Collect stops between volumes when ctx ends, leaving the
// directory consistent.
func (s *Store) Collect(ctx context.Context, policy Policy) (Report, error) {
	return Report{}, errNotImplemented
}

// WaitRemovals waits until every removal Collect started has finished, or ctx
// ends.
func (s *Store) WaitRemovals(ctx context.Context) error {
	return errNotImplemented
}
