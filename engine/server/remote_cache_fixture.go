package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/core"
)

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
