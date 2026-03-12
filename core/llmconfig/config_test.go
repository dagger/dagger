package llmconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	toml "github.com/pelletier/go-toml"
)

func TestConfigSaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			DefaultModel:    "anthropic/claude-sonnet-4.5",
			Providers: map[string]Provider{
				"openrouter": {
					APIKey:  "sk-or-v1-test-key",
					BaseURL: "https://openrouter.ai/api/v1",
					Enabled: true,
				},
				"anthropic": {
					APIKey:  "sk-ant-test-key",
					Enabled: false,
				},
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	if !ConfigExists() {
		t.Fatal("ConfigExists() returned false after Save()")
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if diff := cmp.Diff(cfg, loaded); diff != "" {
		t.Errorf("Loaded config differs from original (-want +got):\n%s", diff)
	}
}

func TestConfigIsTOML(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4.5",
			Providers: map[string]Provider{
				"anthropic": {
					APIKey:  "sk-ant-test",
					Enabled: true,
				},
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Read raw file and verify it's valid TOML
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}

	tree, err := toml.LoadBytes(data)
	if err != nil {
		t.Fatalf("File is not valid TOML: %v", err)
	}

	// Verify structure
	if tree.Get("llm.default_provider").(string) != "anthropic" {
		t.Errorf("expected default_provider = anthropic, got %v", tree.Get("llm.default_provider"))
	}
	if tree.Get("llm.default_model").(string) != "claude-sonnet-4.5" {
		t.Errorf("expected default_model = claude-sonnet-4.5, got %v", tree.Get("llm.default_model"))
	}
}

func TestConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping file permission test on Windows")
	}

	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	info, err := os.Stat(ConfigFile)
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}

	perm := info.Mode().Perm()
	expectedPerm := os.FileMode(0600)
	if perm != expectedPerm {
		t.Errorf("Config file has incorrect permissions: got %o, want %o", perm, expectedPerm)
	}
}

func TestLoadNonExistentConfig(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not error on non-existent config: %v", err)
	}
	if cfg != nil {
		t.Errorf("Load() should return nil for non-existent config, got %+v", cfg)
	}
}

func TestLoadMalformedConfig(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	if err := os.MkdirAll(ConfigRoot, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	if err := os.WriteFile(ConfigFile, []byte("not valid toml [[["), 0600); err != nil {
		t.Fatalf("Failed to write malformed config: %v", err)
	}

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() should error on malformed config, got config: %+v", cfg)
	}
}

func TestConfigRemove(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	if !ConfigExists() {
		t.Fatal("ConfigExists() returned false after Save()")
	}

	if err := Remove(); err != nil {
		t.Fatalf("Remove() failed: %v", err)
	}

	if ConfigExists() {
		t.Fatal("ConfigExists() returned true after Remove()")
	}

	if err := Remove(); err != nil {
		t.Fatalf("Remove() should not error when file doesn't exist: %v", err)
	}
}

func TestLLMConfigured(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	// No file => not configured
	if LLMConfigured() {
		t.Fatal("LLMConfigured() should be false with no file")
	}

	// Empty providers => not configured
	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	if LLMConfigured() {
		t.Fatal("LLMConfigured() should be false with empty providers")
	}

	// With provider => configured
	cfg.LLM.Providers["anthropic"] = Provider{APIKey: "test", Enabled: true}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	if !LLMConfigured() {
		t.Fatal("LLMConfigured() should be true with a provider")
	}
}

func TestConfigConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Initial Save() failed: %v", err)
	}

	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			cfg := &Config{
				LLM: LLMConfig{
					DefaultProvider: "openrouter",
					Providers: map[string]Provider{
						"provider": {
							APIKey:  "test-key",
							Enabled: true,
						},
					},
				},
			}
			done <- cfg.Save()
		}(i)
	}

	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("Concurrent write %d failed: %v", i, err)
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after concurrent writes failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil after concurrent writes")
	}
}

func TestConfigEmptyProviders(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
		},
	}

	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if loaded.LLM.Providers == nil {
		t.Error("Providers map should be initialized after load")
	}

	if len(loaded.LLM.Providers) != 0 {
		t.Errorf("Providers map should be empty, got %d providers", len(loaded.LLM.Providers))
	}
}
