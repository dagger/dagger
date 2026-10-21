// Package userconfig locates and reads the user-level Dagger config file,
// ~/.config/dagger/config.toml or the file named by $DAGGER_CONFIG. The file is
// shared by several subsystems ([llm], [workspaces.*], [engine]); this package
// has no dependencies on them, so the engine client can read its own settings.
package userconfig

import (
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	toml "github.com/pelletier/go-toml"
)

const FileName = "config.toml"

// Path returns the user config file path: $DAGGER_CONFIG when set, otherwise
// config.toml in the dagger directory under $XDG_CONFIG_HOME.
func Path() string {
	if p := os.Getenv("DAGGER_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(xdg.ConfigHome, "dagger", FileName)
}

// LeaveOldEngines reports the [engine] leave_old_engines setting. ok is false
// when the setting is absent, or when the file is missing, unreadable, or
// malformed: a broken config file must never break a command.
func LeaveOldEngines() (leave bool, ok bool) {
	return leaveOldEngines(Path())
}

func leaveOldEngines(path string) (bool, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	doc, err := toml.LoadBytes(data)
	if err != nil {
		return false, false
	}
	leave, ok := doc.GetPath([]string{"engine", "leave_old_engines"}).(bool)
	return leave, ok
}
