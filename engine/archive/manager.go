package archive

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

const ManifestVersion = 3

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

// UnsealedHighWaterHeader carries the cut an unsealed archive is streamed at,
// as "spans,logs,metrics". A sealed archive's cut comes from its bootstrap.
const UnsealedHighWaterHeader = "X-Dagger-Archive-High-Water"

func (h HighWater) String() string {
	return fmt.Sprintf("%d,%d,%d", h.Spans, h.Logs, h.Metrics)
}

// ParseHighWater parses HighWater.String output.
func ParseHighWater(s string) (HighWater, error) {
	var h HighWater
	if n, err := fmt.Sscanf(s, "%d,%d,%d", &h.Spans, &h.Logs, &h.Metrics); err != nil || n != 3 {
		return HighWater{}, fmt.Errorf("invalid archive high-water %q", s)
	}
	if h.Spans < 0 || h.Logs < 0 || h.Metrics < 0 {
		return HighWater{}, fmt.Errorf("invalid archive high-water %q", s)
	}
	return h, nil
}

type HighWater struct {
	Spans   int64 `json:"spans"`
	Logs    int64 `json:"logs"`
	Metrics int64 `json:"metrics"`
}

type Bootstrap struct {
	File    string `json:"file"`
	Records int64  `json:"records"`
	SHA256  string `json:"sha256"`
}

type Manifest struct {
	Version        int        `json:"version"`
	Generation     string     `json:"generation"`
	TraceID        string     `json:"traceID"`
	SourceSession  string     `json:"sourceSession"`
	MainClientID   string     `json:"mainClientID"`
	BoundarySpanID string     `json:"boundarySpanID"`
	State          State      `json:"state"`
	Title          string     `json:"title,omitempty"`
	StartedAt      time.Time  `json:"startedAt"`
	ClosedAt       *time.Time `json:"closedAt,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	SealAt         *time.Time `json:"sealAt,omitempty"`
	SizeBytes      int64      `json:"sizeBytes"`
	Bootstrap      Bootstrap  `json:"bootstrap,omitempty"`
	HighWater      HighWater  `json:"highWater"`
	Failure        string     `json:"failure,omitempty"`
}

type FailureKind string

const (
	FailureAmbiguous FailureKind = "ambiguous"
	FailureNotFound  FailureKind = "not_found"
	FailureEvicted   FailureKind = "evicted"
	FailureState     FailureKind = "state"
	FailureCorrupt   FailureKind = "corrupt"
	FailureIO        FailureKind = "io"
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

type entry struct {
	manifest Manifest
	leases   int
	deleting bool
	removing bool
}

type Manager struct {
	root        string
	ttl         time.Duration
	quota       int64
	now         func() time.Time
	removeStore func(string) (bool, error)

	mu      sync.RWMutex
	entries map[string]*entry
	pending map[string]*entry
	evicted map[string]struct{}
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
		removeStore: cfg.RemoveStore, entries: map[string]*entry{}, pending: map[string]*entry{}, evicted: map[string]struct{}{}, corrupt: map[string]error{},
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func archiveKey(traceID, sourceSession string) string {
	sum := sha256.Sum256([]byte(sourceSession))
	return traceID + "." + hex.EncodeToString(sum[:])
}

func manifestKey(m Manifest) string { return archiveKey(m.TraceID, m.SourceSession) }

// findLocked never resolves a shared trace by arrival order. A generation or
// source selector is exact; a missing generation cannot select another archive.
func (m *Manager) findLocked(traceID, generation, source string) (string, *entry, error) {
	var candidates []string
	var found *entry
	var key string
	for k, ent := range m.entries {
		if ent.deleting || ent.manifest.TraceID != traceID {
			continue
		}
		if source != "" && ent.manifest.SourceSession != source {
			continue
		}
		if generation != "" && ent.manifest.Generation != generation {
			continue
		}
		candidates = append(candidates, fmt.Sprintf("source_session=%q generation=%q", ent.manifest.SourceSession, ent.manifest.Generation))
		key, found = k, ent
	}
	if len(candidates) > 1 {
		sort.Strings(candidates)
		return "", nil, &Failure{Kind: FailureAmbiguous, Err: fmt.Errorf("trace %s has multiple source sessions; select source_session or generation: %s", traceID, strings.Join(candidates, "; "))}
	}
	for k, err := range m.corrupt {
		if (source != "" && k == archiveKey(traceID, source)) || (generation == "" && source == "" && (k == traceID || strings.HasPrefix(k, traceID+"."))) {
			return "", nil, &Failure{Kind: FailureCorrupt, Err: err}
		}
	}
	if found != nil {
		return key, found, nil
	}
	if generation != "" {
		return "", nil, &Failure{Kind: FailureCorrupt, Err: errors.New("requested archive generation is unavailable for this source")}
	}
	for k := range m.evicted {
		if (source != "" && k == archiveKey(traceID, source)) || (source == "" && strings.HasPrefix(k, traceID+".")) {
			return "", nil, &Failure{Kind: FailureEvicted}
		}
	}
	return "", nil, &Failure{Kind: FailureNotFound}
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
		if manifestKey(manifest) != traceID {
			m.corrupt[traceID] = errors.New("archive manifest filename and trace identity differ")
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
		copy := manifest
		m.entries[traceID] = &entry{manifest: copy}
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
	if manifest.Generation == "" || manifest.SourceSession == "" || manifest.MainClientID == "" || manifest.BoundarySpanID == "" {
		return errors.New("missing immutable identity")
	}
	if filepath.Base(manifest.MainClientID) != manifest.MainClientID || manifest.MainClientID == "." || manifest.MainClientID == ".." {
		return errors.New("invalid archive store identity")
	}
	if manifest.Bootstrap.File != "" && manifest.Bootstrap.File != manifestKey(manifest)+".bootstrap" {
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

func randomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (m *Manager) Register(traceID, mainClientID string) (Manifest, error) {
	return m.RegisterSession(traceID, mainClientID, mainClientID)
}

func (m *Manager) RegisterSession(traceID, sourceSession, mainClientID string) (Manifest, error) {
	if _, err := trace.TraceIDFromHex(traceID); err != nil {
		return Manifest{}, fmt.Errorf("invalid canonical trace ID: %w", err)
	}
	if mainClientID == "" {
		return Manifest{}, errors.New("main client ID is required")
	}
	generation, err := randomHex(16)
	if err != nil {
		return Manifest{}, err
	}
	boundary, err := randomHex(8)
	if err != nil {
		return Manifest{}, err
	}
	now := m.now().UTC()
	manifest := Manifest{
		Version: ManifestVersion, Generation: generation, TraceID: traceID, SourceSession: sourceSession,
		MainClientID: mainClientID, BoundarySpanID: boundary, State: StateActive,
		StartedAt: now, ExpiresAt: now.Add(m.ttl),
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := manifestKey(manifest)
	for _, entries := range []map[string]*entry{m.entries, m.pending} {
		for _, old := range entries {
			if old.manifest.MainClientID == mainClientID && old.manifest.SourceSession != sourceSession {
				return Manifest{}, errors.New("archive telemetry store is already bound to another source session")
			}
		}
	}
	if _, exists := m.entries[key]; exists {
		return Manifest{}, fmt.Errorf("archive trace %s source session %s already registered", traceID, sourceSession)
	}
	if _, deleting := m.pending[key]; deleting {
		return Manifest{}, fmt.Errorf("archive trace %s source session %s is pending deletion", traceID, sourceSession)
	}
	if err := m.corrupt[key]; err != nil {
		return Manifest{}, &Failure{Kind: FailureCorrupt, Err: err}
	}
	if err := m.writeManifest(manifest); err != nil {
		return Manifest{}, err
	}
	m.entries[key] = &entry{manifest: manifest}
	delete(m.evicted, key)
	return manifest, nil
}

func (m *Manager) BeginFinalizing(traceID, generation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, err := m.mutable(traceID, generation, StateActive)
	if err != nil {
		return err
	}
	next := ent.manifest
	next.State = StateFinalizing
	if err := m.writeManifest(next); err != nil {
		return err
	}
	ent.manifest = next
	return nil
}

type FinalizeInput struct {
	HighWater        HighWater
	SealAt           time.Time
	StoreSizeBytes   int64
	BootstrapBytes   []byte
	BootstrapRecords int64
	// Title is the latest session title recorded in the trace at the cut. It
	// is sanitized like SetTitle; an empty title keeps the current one.
	Title string
}

func (m *Manager) Finalize(traceID, generation string, in FinalizeInput) (Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, err := m.mutable(traceID, generation, StateFinalizing)
	if err != nil {
		return Manifest{}, err
	}
	now := m.now().UTC()
	sealAt := in.SealAt.UTC()
	if sealAt.IsZero() {
		sealAt = now
	}
	header, terminal, err := VerifyBootstrap(bytes.NewReader(in.BootstrapBytes))
	if err != nil {
		return Manifest{}, fmt.Errorf("verify archive bootstrap: %w", err)
	}
	if header.SourceSession != ent.manifest.SourceSession || header.Generation != generation || header.TraceID != traceID || header.HighWater != in.HighWater || header.SealAt != sealAt.Format(time.RFC3339Nano) {
		return Manifest{}, errors.New("bootstrap fixed cut does not match archive finalization")
	}
	records := terminal.TraceRecords + terminal.LogRecords
	if in.BootstrapRecords != 0 && in.BootstrapRecords != records {
		return Manifest{}, fmt.Errorf("bootstrap record count is %d, want %d", in.BootstrapRecords, records)
	}
	sidecar := manifestKey(ent.manifest) + ".bootstrap"
	digest := sha256.Sum256(in.BootstrapBytes)
	if err := atomicWrite(filepath.Join(m.root, sidecar), in.BootstrapBytes, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("write archive bootstrap: %w", err)
	}
	ent.manifest.State = StateClosed
	ent.manifest.ClosedAt = &now
	ent.manifest.ExpiresAt = now.Add(m.ttl)
	ent.manifest.SealAt = &sealAt
	ent.manifest.HighWater = in.HighWater
	ent.manifest.Bootstrap = Bootstrap{File: sidecar, Records: records, SHA256: hex.EncodeToString(digest[:])}
	if title := SanitizeTitle(in.Title); title != "" {
		ent.manifest.Title = title
	}
	baseSize := in.StoreSizeBytes + int64(len(in.BootstrapBytes))
	ent.manifest.SizeBytes = baseSize
	for range 2 {
		manifestBytes, err := json.Marshal(ent.manifest)
		if err != nil {
			return Manifest{}, err
		}
		ent.manifest.SizeBytes = baseSize + int64(len(manifestBytes))
	}
	ent.manifest.Failure = ""
	if err := m.writeManifest(ent.manifest); err != nil {
		ent.manifest.State = StateIncomplete
		ent.manifest.Failure = err.Error()
		_ = m.writeManifest(ent.manifest)
		return Manifest{}, err
	}
	return ent.manifest, nil
}

func (m *Manager) MarkIncomplete(traceID, generation string, cause error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ent, err := m.findLocked(traceID, generation, "")
	if err != nil {
		return err
	}
	ent.manifest.State = StateIncomplete
	if cause != nil {
		ent.manifest.Failure = cause.Error()
	}
	return m.writeManifest(ent.manifest)
}

// SetTitle records the latest session title the engine derived from an active
// archive's trace. It is persisted immediately, so an archive recovered after
// an engine crash keeps it. The title is sanitized first; an unchanged or
// empty title writes nothing.
func (m *Manager) SetTitle(traceID, generation, title string) error {
	title = SanitizeTitle(title)
	m.mu.Lock()
	defer m.mu.Unlock()
	ent, err := m.mutable(traceID, generation, StateActive)
	if err != nil {
		return err
	}
	if title == "" || title == ent.manifest.Title {
		return nil
	}
	next := ent.manifest
	next.Title = title
	if err := m.writeManifest(next); err != nil {
		return err
	}
	ent.manifest = next
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

func (m *Manager) Discard(traceID, generation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ent, err := m.findLocked(traceID, generation, "")
	if err != nil {
		return err
	}
	delete(m.entries, key)
	if ent.manifest.Bootstrap.File != "" {
		_ = os.Remove(filepath.Join(m.root, ent.manifest.Bootstrap.File))
	}
	return removeAndSync(filepath.Join(m.root, key+".json"))
}

func (m *Manager) mutable(traceID, generation string, want State) (*entry, error) {
	_, ent, err := m.findLocked(traceID, generation, "")
	if err != nil {
		return nil, err
	}
	if ent.manifest.State != want {
		return nil, &Failure{Kind: FailureState, State: ent.manifest.State}
	}
	return ent, nil
}

type Lease struct {
	manager  *Manager
	entry    *entry
	manifest Manifest
	once     sync.Once
}

func (l *Lease) Manifest() Manifest { return l.manifest }
func (l *Lease) BootstrapPath() string {
	return filepath.Join(l.manager.root, l.manifest.Bootstrap.File)
}
func (l *Lease) Release() { l.once.Do(func() { l.manager.release(l.entry) }) }

func (m *Manager) Acquire(traceID string) (*Lease, error) {
	return m.AcquireSource(traceID, "", "")
}

func (m *Manager) AcquireSource(traceID, generation, sourceSession string) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ent, err := m.findLocked(traceID, generation, sourceSession)
	if err != nil {
		return nil, err
	}
	if ent.manifest.State != StateClosed {
		return nil, &Failure{Kind: FailureState, State: ent.manifest.State}
	}
	return m.leaseLocked(ent)
}

// AcquireUnsealed leases an archive that was never sealed: its engine stopped
// before graceful finalization (interrupted), or finalization failed
// (incomplete). Such an archive has no bootstrap, completion witness or fixed
// cut. Its session is gone, so nothing writes to it anymore, and readers may
// stream what it recorded for a best-effort restore.
func (m *Manager) AcquireUnsealed(traceID, generation, sourceSession string) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ent, err := m.findLocked(traceID, generation, sourceSession)
	if err != nil {
		return nil, err
	}
	if !ent.manifest.State.Unsealed() {
		return nil, &Failure{Kind: FailureState, State: ent.manifest.State}
	}
	return m.leaseLocked(ent)
}

func (m *Manager) leaseLocked(ent *entry) (*Lease, error) {
	if ent.leases == 0 && !ent.manifest.ExpiresAt.After(m.now()) {
		return nil, &Failure{Kind: FailureEvicted}
	}
	ent.leases++
	return &Lease{manager: m, entry: ent, manifest: ent.manifest}, nil
}

func (m *Manager) release(ent *entry) {
	m.mu.Lock()
	ent.leases--
	deleting := ent.deleting && ent.leases == 0 && !ent.removing
	if deleting {
		ent.removing = true
	}
	m.mu.Unlock()
	if deleting {
		_ = m.deleteEntry(ent)
	}
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
	for key, ent := range m.entries {
		if !ent.deleting && ent.manifest.TraceID != excludeTraceID && ent.manifest.Generation != excludeTraceID && key > after {
			manifest := ent.manifest
			if manifest.Title == "" {
				manifest.Title = "Agent session " + manifest.StartedAt.Format(time.RFC3339)
			}
			all = append(all, manifest)
		}
	}
	m.mu.RUnlock()
	sort.Slice(all, func(i, j int) bool { return manifestKey(all[i]) < manifestKey(all[j]) })
	page := Page{Archives: all}
	if len(page.Archives) > limit {
		page.Archives = page.Archives[:limit]
		page.Next = manifestKey(page.Archives[len(page.Archives)-1])
	}
	return page
}

func (m *Manager) Manifest(traceID string) (Manifest, error) {
	return m.ManifestSource(traceID, "", "")
}

func (m *Manager) ManifestSource(traceID, generation, sourceSession string) (Manifest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ent, err := m.findLocked(traceID, generation, sourceSession)
	if err != nil {
		return Manifest{}, err
	}
	return ent.manifest, nil
}

func (m *Manager) KeepSet() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keep := make(map[string]bool, len(m.entries))
	for _, ent := range m.entries {
		if !ent.deleting {
			keep[ent.manifest.MainClientID] = true
		}
	}
	return keep
}

func (m *Manager) markDeletingLocked(ent *entry) {
	ent.deleting = true
	key := manifestKey(ent.manifest)
	delete(m.entries, key)
	m.pending[key] = ent
	m.evicted[key] = struct{}{}
}

func (m *Manager) GC() (overage int64, err error) {
	now := m.now()
	m.mu.Lock()
	closed := make([]*entry, 0, len(m.entries))
	for _, ent := range m.entries {
		switch ent.manifest.State {
		case StateClosed:
			if !ent.deleting {
				closed = append(closed, ent)
			}
		case StateInterrupted, StateIncomplete:
			if !ent.deleting && !ent.manifest.ExpiresAt.After(now) {
				m.markDeletingLocked(ent)
			}
		}
	}
	sort.Slice(closed, func(i, j int) bool {
		return closed[i].manifest.ClosedAt.Before(*closed[j].manifest.ClosedAt)
	})
	var ready []*entry
	var retained int64
	var newest *entry
	for _, ent := range closed {
		if ent.leases == 0 && !ent.manifest.ExpiresAt.After(now) {
			m.markDeletingLocked(ent)
			continue
		}
		newest = ent
		retained += ent.manifest.SizeBytes
	}
	// Keep the newest non-expired closed archive even when it alone exceeds the
	// soft quota. Older archives are selected until the target is reached.
	for _, ent := range closed {
		if retained <= m.quota || ent.deleting || ent == newest || ent.leases > 0 {
			continue
		}
		m.markDeletingLocked(ent)
		retained -= ent.manifest.SizeBytes
	}
	if retained > m.quota {
		overage = retained - m.quota
	}
	for _, ent := range m.pending {
		if ent.leases == 0 && !ent.removing {
			ent.removing = true
			ready = append(ready, ent)
		}
	}
	m.mu.Unlock()
	for _, ent := range ready {
		err = errors.Join(err, m.deleteEntry(ent))
	}
	return overage, err
}

func (m *Manager) deleteEntry(ent *entry) (rerr error) {
	complete := false
	defer func() {
		m.mu.Lock()
		ent.removing = false
		if complete {
			delete(m.pending, manifestKey(ent.manifest))
		}
		m.mu.Unlock()
	}()
	if m.removeStore != nil {
		removed, err := m.removeStore(ent.manifest.MainClientID)
		if err != nil {
			return err
		}
		if !removed {
			return nil
		}
	}
	var result error
	if ent.manifest.Bootstrap.File != "" {
		if err := os.Remove(filepath.Join(m.root, ent.manifest.Bootstrap.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	result = errors.Join(result, removeAndSync(filepath.Join(m.root, manifestKey(ent.manifest)+".json")))
	complete = result == nil
	return result
}

func (m *Manager) writeManifest(manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(m.root, manifestKey(manifest)+".json"), data, 0o600)
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
