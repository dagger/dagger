package idtui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
)

const (
	cacheImpactStoreVersion = 3
	maxCacheImpactProfiles  = 100
)

var cacheImpactFile = filepath.Join(xdgCacheHome(), "dagger", "cache-impact.json")

func xdgCacheHome() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return dir
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return os.TempDir()
	}
	return dir
}

type cacheRunProfile struct {
	Key              string        `json:"key"`
	Elapsed          time.Duration `json:"elapsed"`
	CPU              time.Duration `json:"cpu,omitempty"`
	NetworkBytes     int64         `json:"networkBytes,omitempty"`
	CPUAvailable     bool          `json:"cpuAvailable,omitempty"`
	NetworkAvailable bool          `json:"networkAvailable,omitempty"`
	Hits             int           `json:"hits"`
	Lookups          int           `json:"lookups"`
	RecordedAt       time.Time     `json:"recordedAt"`
}

type cacheImpactStore struct {
	Version  int                        `json:"version"`
	Profiles map[string]cacheRunProfile `json:"profiles"`
}

type cacheImpact struct {
	Elapsed      time.Duration
	Percent      float64
	CPU          time.Duration
	NetworkBytes int64
	HasCPU       bool
	HasNetwork   bool
	BaselineRate float64
}

// prepareCacheImpact compares this run with a prior cache-cold run of the same
// outer workflow. Comparing makespans (rather than summing call durations)
// correctly accounts for parallel branches and unrelated concurrent work.
func (fe *frontendPretty) prepareCacheImpact() {
	if fe.cacheImpactPrepared {
		return
	}
	fe.cacheImpactPrepared = true

	current, ok := cacheProfile(fe.db)
	if !ok {
		return
	}
	stats := fe.db.CacheStats(nil)
	current.Hits = stats.Hits
	current.Lookups = stats.Lookups()
	baseline, found, err := loadCacheImpactProfile(cacheImpactFile, current.Key)
	if err == nil && found && cacheHitRate(current) > cacheHitRate(baseline) {
		impact := cacheImpact{
			Elapsed:      max(0, baseline.Elapsed-current.Elapsed),
			BaselineRate: cacheHitRate(baseline) * 100,
		}
		if baseline.Elapsed > 0 {
			impact.Percent = float64(impact.Elapsed) / float64(baseline.Elapsed) * 100
		}
		if baseline.CPUAvailable && current.CPUAvailable {
			impact.HasCPU = true
			impact.CPU = max(0, baseline.CPU-current.CPU)
		}
		if baseline.NetworkAvailable && current.NetworkAvailable {
			impact.HasNetwork = true
			impact.NetworkBytes = max(0, baseline.NetworkBytes-current.NetworkBytes)
		}
		if impact.Elapsed > 0 || impact.CPU > 0 || impact.NetworkBytes > 0 {
			fe.cacheImpact = &impact
		}
	}

	// Keep the coldest compatible run observed locally. Real workflows include
	// bootstrap calls which may already be cached, so requiring a literal zero
	// hit run would make a baseline practically unobtainable. The report calls
	// this a "colder run" and only attributes the measured difference between
	// the two observations.
	if stats.Executed > 0 && (!found || cacheHitRate(current) < cacheHitRate(baseline)) {
		_ = saveCacheImpactProfile(cacheImpactFile, current)
	}
}

func cacheHitRate(profile cacheRunProfile) float64 {
	if profile.Lookups == 0 {
		return 0
	}
	return float64(profile.Hits) / float64(profile.Lookups)
}

func cacheProfile(db *dagui.DB) (cacheRunProfile, bool) {
	primary := db.Spans.Map[db.PrimarySpan]
	if primary == nil || !primary.Received || primary.Name == "" {
		return cacheRunProfile{}, false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return cacheRunProfile{}, false
	}
	h := sha256.New()
	h.Write([]byte(cwd))
	h.Write([]byte{0})
	h.Write([]byte(primary.Name))

	var (
		start time.Time
		end   time.Time
	)
	for _, span := range db.Spans.Order {
		if !span.Received || span.CacheContract != telemetryattrs.CacheContractV1 {
			continue
		}
		if start.IsZero() || span.StartTime.Before(start) {
			start = span.StartTime
		}
		if span.EndTime.After(end) {
			end = span.EndTime
		}
	}
	if start.IsZero() || !end.After(start) {
		return cacheRunProfile{}, false
	}

	profile := cacheRunProfile{
		Key:        hex.EncodeToString(h.Sum(nil)),
		Elapsed:    end.Sub(start),
		RecordedAt: time.Now().UTC(),
	}
	for _, metrics := range db.MetricsByCall {
		if value, ok := lastMetric(metrics[telemetry.CPUStatUsage]); ok {
			profile.CPUAvailable = true
			profile.CPU += time.Duration(value) * time.Microsecond
		}
		var callNetwork int64
		var networkAvailable bool
		if value, ok := lastMetric(metrics[telemetry.NetstatRxBytes]); ok {
			networkAvailable = true
			callNetwork += value
		}
		if value, ok := lastMetric(metrics[telemetry.NetstatTxBytes]); ok {
			networkAvailable = true
			callNetwork += value
		}
		if networkAvailable {
			profile.NetworkAvailable = true
			profile.NetworkBytes += callNetwork
		}
	}
	return profile, true
}

func lastMetric(points []metricdata.DataPoint[int64]) (int64, bool) {
	if len(points) == 0 {
		return 0, false
	}
	return points[len(points)-1].Value, true
}

func loadCacheImpactProfile(path, key string) (cacheRunProfile, bool, error) {
	store, err := readCacheImpactStore(path)
	if err != nil {
		return cacheRunProfile{}, false, err
	}
	profile, ok := store.Profiles[key]
	return profile, ok, nil
}

func readCacheImpactStore(path string) (cacheImpactStore, error) {
	store := cacheImpactStore{Version: cacheImpactStoreVersion, Profiles: map[string]cacheRunProfile{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return store, err
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return cacheImpactStore{}, err
	}
	if store.Version != cacheImpactStoreVersion {
		return cacheImpactStore{Version: cacheImpactStoreVersion, Profiles: map[string]cacheRunProfile{}}, nil
	}
	if store.Profiles == nil {
		store.Profiles = map[string]cacheRunProfile{}
	}
	return store, nil
}

func saveCacheImpactProfile(path string, profile cacheRunProfile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Unlock() //nolint:errcheck

	store, err := readCacheImpactStore(path)
	if err != nil {
		// A corrupt optional cache must not break the command; replace it with a
		// fresh versioned store while holding the lock.
		store = cacheImpactStore{Version: cacheImpactStoreVersion, Profiles: map[string]cacheRunProfile{}}
	}
	store.Profiles[profile.Key] = profile
	pruneCacheImpactProfiles(store.Profiles)
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cache-impact-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func pruneCacheImpactProfiles(profiles map[string]cacheRunProfile) {
	for len(profiles) > maxCacheImpactProfiles {
		var oldestKey string
		var oldest time.Time
		for key, profile := range profiles {
			if oldestKey == "" || profile.RecordedAt.Before(oldest) {
				oldestKey, oldest = key, profile.RecordedAt
			}
		}
		delete(profiles, oldestKey)
	}
}
