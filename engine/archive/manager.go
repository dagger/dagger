package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/otel/trace"
)

const ManifestVersion = 4

const (
	DefaultTTL   = 7 * 24 * time.Hour
	DefaultQuota = int64(10 << 30)
)

type State string

const (
	StateActive      State = "active"
	StateFinalizing  State = "finalizing"
	StateClosed      State = "closed"
	StateInterrupted State = "interrupted"
	StateIncomplete  State = "incomplete"
)

// Unsealed reports whether an archive's session ended without a verified
// seal: the engine stopped before finalizing it, or finalization failed.
func (s State) Unsealed() bool {
	return s == StateInterrupted || s == StateIncomplete
}

type HighWater struct {
	Spans   int64 `json:"spans"`
	Logs    int64 `json:"logs"`
	Metrics int64 `json:"metrics"`
}

type Bootstrap struct {
	File    string `json:"file"`
	Records int64  `json:"records"`
}

// Manifest describes one archive. An archive is identified by its trace ID
// alone: the first session to register a trace owns its archive.
type Manifest struct {
	Version      int        `json:"version"`
	TraceID      string     `json:"traceID"`
	MainClientID string     `json:"mainClientID"`
	State        State      `json:"state"`
	Title        string     `json:"title,omitempty"`
	StartedAt    time.Time  `json:"startedAt"`
	ClosedAt     *time.Time `json:"closedAt,omitempty"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	SealAt       *time.Time `json:"sealAt,omitempty"`
	SizeBytes    int64      `json:"sizeBytes"`
	Bootstrap    Bootstrap  `json:"bootstrap,omitempty"`
	HighWater    HighWater  `json:"highWater"`
	Failure      string     `json:"failure,omitempty"`
}

type FailureKind string

const (
	FailureNotFound FailureKind = "not_found"
	FailureState    FailureKind = "state"
	FailureCorrupt  FailureKind = "corrupt"
	FailureIO       FailureKind = "io"
)

type Failure struct {
	Kind  FailureKind `json:"kind"`
	State State       `json:"state,omitempty"`
	Err   error       `json:"-"`
}

func (e *Failure) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("archive %s: %v", e.Kind, e.Err)
	}
	if e.State != "" {
		return fmt.Sprintf("archive is %s", e.State)
	}
	return "archive " + string(e.Kind)
}
func (e *Failure) Unwrap() error { return e.Err }

type Config struct {
	Root        string
	TTL         time.Duration
	QuotaBytes  int64
	Now         func() time.Time
	RemoveStore func(string) (bool, error)
}

type Manager struct {
	root        string
	ttl         time.Duration
	quota       int64
	now         func() time.Time
	removeStore func(string) (bool, error)

	mu      sync.RWMutex
	entries map[string]*Manifest
	corrupt map[string]error
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.Root == "" {
		return nil, errors.New("archive root is required")
	}
	if cfg.TTL == 0 {
		cfg.TTL = DefaultTTL
	}
	if cfg.TTL < 0 {
		return nil, errors.New("archive TTL must be positive")
	}
	if cfg.QuotaBytes == 0 {
		cfg.QuotaBytes = DefaultQuota
	}
	if cfg.QuotaBytes < 0 {
		return nil, errors.New("archive quota must be positive")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if err := os.MkdirAll(cfg.Root, 0o700); err != nil {
		return nil, fmt.Errorf("create archive directory: %w", err)
	}
	m := &Manager{
		root: cfg.Root, ttl: cfg.TTL, quota: cfg.QuotaBytes, now: cfg.Now,
		removeStore: cfg.RemoveStore, entries: map[string]*Manifest{}, corrupt: map[string]error{},
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) findLocked(traceID string) (*Manifest, error) {
	if manifest, ok := m.entries[traceID]; ok {
		return manifest, nil
	}
	if err := m.corrupt[traceID]; err != nil {
		return nil, &Failure{Kind: FailureCorrupt, Err: err}
	}
	return nil, &Failure{Kind: FailureNotFound}
}

func (m *Manager) load() error {
	files, err := os.ReadDir(m.root)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		path := filepath.Join(m.root, file.Name())
		traceID := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
		data, err := os.ReadFile(path)
		if err != nil {
			m.corrupt[traceID] = fmt.Errorf("read archive manifest %s: %w", file.Name(), err)
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			m.corrupt[traceID] = fmt.Errorf("decode archive manifest %s: %w", file.Name(), err)
			continue
		}
		if manifest.TraceID != traceID {
			m.corrupt[traceID] = errors.New("archive manifest filename and trace ID differ")
			continue
		}
		if err := validateManifest(manifest); err != nil {
			m.corrupt[traceID] = fmt.Errorf("invalid archive manifest %s: %w", file.Name(), err)
			continue
		}
		if manifest.State == StateActive || manifest.State == StateFinalizing {
			manifest.State = StateInterrupted
			manifest.Failure = "engine stopped before graceful archive finalization"
			if err := m.writeManifest(manifest); err != nil {
				return err
			}
		}
		m.entries[traceID] = &manifest
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("unsupported version %d", manifest.Version)
	}
	if _, err := trace.TraceIDFromHex(manifest.TraceID); err != nil {
		return fmt.Errorf("trace ID: %w", err)
	}
	if manifest.MainClientID == "" {
		return errors.New("missing main client ID")
	}
	if filepath.Base(manifest.MainClientID) != manifest.MainClientID || manifest.MainClientID == "." || manifest.MainClientID == ".." {
		return errors.New("invalid archive store identity")
	}
	if manifest.Bootstrap.File != "" && manifest.Bootstrap.File != manifest.TraceID+".bootstrap" {
		return errors.New("invalid bootstrap filename")
	}
	if manifest.HighWater.Spans < 0 || manifest.HighWater.Logs < 0 || manifest.HighWater.Metrics < 0 || manifest.SizeBytes < 0 {
		return errors.New("invalid archive bounds")
	}
	switch manifest.State {
	case StateActive, StateFinalizing, StateClosed, StateInterrupted, StateIncomplete:
	default:
		return fmt.Errorf("unknown state %q", manifest.State)
	}
	if manifest.State == StateClosed && (manifest.ClosedAt == nil || manifest.SealAt == nil || manifest.Bootstrap.File == "") {
		return errors.New("closed archive is missing its finalized cut")
	}
	return nil
}

// Register creates the active archive for a trace. The first session to
// register a trace owns its archive; a later registration for the same trace
// (e.g. a nested session that inherited TRACEPARENT) fails.
func (m *Manager) Register(traceID, mainClientID string) (Manifest, error) {
	if _, err := trace.TraceIDFromHex(traceID); err != nil {
		return Manifest{}, fmt.Errorf("invalid canonical trace ID: %w", err)
	}
	if mainClientID == "" {
		return Manifest{}, errors.New("main client ID is required")
	}
	now := m.now().UTC()
	manifest := Manifest{
		Version: ManifestVersion, TraceID: traceID, MainClientID: mainClientID,
		State: StateActive, StartedAt: now, ExpiresAt: now.Add(m.ttl),
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[traceID]; exists {
		return Manifest{}, fmt.Errorf("archive for trace %s is already registered", traceID)
	}
	if err := m.corrupt[traceID]; err != nil {
		return Manifest{}, &Failure{Kind: FailureCorrupt, Err: err}
	}
	if err := m.writeManifest(manifest); err != nil {
		return Manifest{}, err
	}
	m.entries[traceID] = &manifest
	return manifest, nil
}

func (m *Manager) BeginFinalizing(traceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.mutable(traceID, StateActive)
	if err != nil {
		return err
	}
	next := *current
	next.State = StateFinalizing
	if err := m.writeManifest(next); err != nil {
		return err
	}
	*current = next
	return nil
}

// FinalizeInput is the sealed cut. The caller built and verified the
// bootstrap for exactly this cut.
type FinalizeInput struct {
	HighWater        HighWater
	SealAt           time.Time
	StoreSizeBytes   int64
	BootstrapBytes   []byte
	BootstrapRecords int64
}

func (m *Manager) Finalize(traceID string, in FinalizeInput) (Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, err := m.mutable(traceID, StateFinalizing)
	if err != nil {
		return Manifest{}, err
	}
	now := m.now().UTC()
	sealAt := in.SealAt.UTC()
	if sealAt.IsZero() {
		sealAt = now
	}
	sidecar := traceID + ".bootstrap"
	if err := atomicWrite(filepath.Join(m.root, sidecar), in.BootstrapBytes, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("write archive bootstrap: %w", err)
	}
	ent.State = StateClosed
	ent.ClosedAt = &now
	ent.ExpiresAt = now.Add(m.ttl)
	ent.SealAt = &sealAt
	ent.HighWater = in.HighWater
	ent.Bootstrap = Bootstrap{File: sidecar, Records: in.BootstrapRecords}
	ent.SizeBytes = in.StoreSizeBytes + int64(len(in.BootstrapBytes))
	ent.Failure = ""
	if err := m.writeManifest(*ent); err != nil {
		ent.State = StateIncomplete
		ent.Failure = err.Error()
		_ = m.writeManifest(*ent)
		return Manifest{}, err
	}
	return *ent, nil
}

func (m *Manager) MarkIncomplete(traceID string, cause error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, err := m.findLocked(traceID)
	if err != nil {
		return err
	}
	ent.State = StateIncomplete
	if cause != nil {
		ent.Failure = cause.Error()
	}
	return m.writeManifest(*ent)
}

// SetTitle records the session title the main client published into an active
// archive's trace. It is persisted immediately, so an archive recovered after
// an engine crash keeps it, and the sealed manifest inherits it. The title is
// sanitized first; an unchanged or empty title writes nothing.
func (m *Manager) SetTitle(traceID, title string) error {
	title = SanitizeTitle(title)
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, err := m.mutable(traceID, StateActive)
	if err != nil {
		return err
	}
	if title == "" || title == ent.Title {
		return nil
	}
	next := *ent
	next.Title = title
	if err := m.writeManifest(next); err != nil {
		return err
	}
	*ent = next
	return nil
}

// MaxTitleRunes bounds an archive title. Titles come from trace records any
// session producer can emit, and listings print them to a terminal.
const MaxTitleRunes = 120

// SanitizeTitle reduces a trace-derived title to one bounded, printable line:
// control and format characters (including bidi overrides) are dropped or
// turned into spaces, whitespace runs collapse, and long titles are cut with an
// ellipsis.
func SanitizeTitle(title string) string {
	// Bound the work before normalizing; the rune cut below is authoritative.
	truncated := false
	if limit := MaxTitleRunes * utf8.UTFMax; len(title) > limit {
		title, truncated = title[:limit], true
	}
	title = strings.ToValidUTF8(title, "")
	title = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r), unicode.IsControl(r):
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	runes := []rune(title)
	if len(runes) > MaxTitleRunes {
		truncated = true
	}
	if !truncated || title == "" {
		return title
	}
	if len(runes) > MaxTitleRunes-1 {
		runes = runes[:MaxTitleRunes-1]
	}
	return strings.TrimSpace(string(runes)) + "…"
}

func (m *Manager) mutable(traceID string, want State) (*Manifest, error) {
	ent, err := m.findLocked(traceID)
	if err != nil {
		return nil, err
	}
	if ent.State != want {
		return nil, &Failure{Kind: FailureState, State: ent.State}
	}
	return ent, nil
}

// BootstrapPath is where a sealed archive's agent bootstrap is stored.
func (m *Manager) BootstrapPath(traceID string) string {
	return filepath.Join(m.root, traceID+".bootstrap")
}

type Page struct {
	Archives []Manifest `json:"archives"`
	Next     string     `json:"next,omitempty"`
}

func (m *Manager) List(after, excludeTraceID string, limit int) Page {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	m.mu.RLock()
	all := make([]Manifest, 0, len(m.entries))
	for traceID, ent := range m.entries {
		if traceID != excludeTraceID && traceID > after {
			manifest := *ent
			if manifest.Title == "" {
				manifest.Title = "Agent session " + manifest.StartedAt.Format(time.RFC3339)
			}
			all = append(all, manifest)
		}
	}
	m.mu.RUnlock()
	sort.Slice(all, func(i, j int) bool { return all[i].TraceID < all[j].TraceID })
	page := Page{Archives: all}
	if len(page.Archives) > limit {
		page.Archives = page.Archives[:limit]
		page.Next = page.Archives[len(page.Archives)-1].TraceID
	}
	return page
}

func (m *Manager) Manifest(traceID string) (Manifest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ent, err := m.findLocked(traceID)
	if err != nil {
		return Manifest{}, err
	}
	return *ent, nil
}

func (m *Manager) KeepSet() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keep := make(map[string]bool, len(m.entries))
	for _, ent := range m.entries {
		keep[ent.MainClientID] = true
	}
	return keep
}

// GC deletes expired archives, then the oldest closed archives until the
// retained ones fit the quota. The newest closed archive is kept even when it
// alone exceeds the quota; overage reports by how much. An archive whose store
// is still open is left in place and retried by the next GC.
func (m *Manager) GC() (overage int64, err error) {
	now := m.now()
	var doomed, closed []Manifest
	m.mu.RLock()
	for _, ent := range m.entries {
		switch {
		case ent.State != StateClosed && !ent.State.Unsealed():
			// Active or finalizing: owned by a live session.
		case !ent.ExpiresAt.After(now):
			doomed = append(doomed, *ent)
		case ent.State == StateClosed:
			closed = append(closed, *ent)
		}
	}
	m.mu.RUnlock()
	sort.Slice(closed, func(i, j int) bool {
		return closed[i].ClosedAt.Before(*closed[j].ClosedAt)
	})
	var retained int64
	for _, manifest := range closed {
		retained += manifest.SizeBytes
	}
	for _, manifest := range closed[:max(len(closed)-1, 0)] {
		if retained <= m.quota {
			break
		}
		doomed = append(doomed, manifest)
		retained -= manifest.SizeBytes
	}
	if retained > m.quota {
		overage = retained - m.quota
	}
	for _, manifest := range doomed {
		err = errors.Join(err, m.delete(manifest))
	}
	return overage, err
}

func (m *Manager) delete(manifest Manifest) error {
	if m.removeStore != nil {
		removed, err := m.removeStore(manifest.MainClientID)
		if err != nil || !removed {
			return err
		}
	}
	var result error
	if manifest.Bootstrap.File != "" {
		if err := os.Remove(filepath.Join(m.root, manifest.Bootstrap.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	result = errors.Join(result, removeAndSync(filepath.Join(m.root, manifest.TraceID+".json")))
	if result == nil {
		m.mu.Lock()
		delete(m.entries, manifest.TraceID)
		m.mu.Unlock()
	}
	return result
}

func (m *Manager) writeManifest(manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(m.root, manifest.TraceID+".json"), data, 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) (rerr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if rerr != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func removeAndSync(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
