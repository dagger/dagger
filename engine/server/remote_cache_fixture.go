package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/containerd/platforms"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/core"
)

// remoteCacheFixtureDefaultPlatformEnv sets a fixture engine's default
// platform, so that a test can make engines of different platforms exchange
// results on one machine. It is read only when the fixture is enabled.
const remoteCacheFixtureDefaultPlatformEnv = "_DAGGER_TEST_REMOTE_CACHE_FIXTURE_DEFAULT_PLATFORM"

// remoteCacheFixtureDefaultPlatform returns the default platform a fixture
// engine was started with, if any.
func remoteCacheFixtureDefaultPlatform() (ocispecs.Platform, bool, error) {
	if os.Getenv(core.RemoteCacheFixtureRootEnv) == "" {
		return ocispecs.Platform{}, false, nil
	}
	spec := os.Getenv(remoteCacheFixtureDefaultPlatformEnv)
	if spec == "" {
		return ocispecs.Platform{}, false, nil
	}
	platform, err := platforms.Parse(spec)
	if err != nil {
		return ocispecs.Platform{}, false, err
	}
	return platforms.Normalize(platform), true, nil
}

// Called during boot or under gcmu. The fixture directory survives a worker
// reset, so the next report can distinguish pruning from discarded persistence.
// No fixture files are accessed unless the test gate is enabled.
func (srv *Server) updateRemoteCacheFixturePersistence(update func(*core.RemoteCacheFixturePersistence)) error {
	path := os.Getenv(core.RemoteCacheFixtureRootEnv)
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		return errors.New("remote cache fixture root must be absolute")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	var report core.RemoteCacheFixturePersistence
	raw, err := root.ReadFile("persistence.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &report); err != nil {
			return err
		}
	}
	update(&report)
	raw, err = json.Marshal(report)
	if err != nil {
		return err
	}
	if err := root.WriteFile(".persistence.tmp", raw, 0600); err != nil {
		return err
	}
	return root.Rename(".persistence.tmp", "persistence.json")
}
