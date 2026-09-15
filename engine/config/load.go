package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func Load(r io.Reader) (Config, error) {
	dec := json.NewDecoder(r)
	// explicitly disallow unknown fields - this should help prevent
	// accidentally applying a new config to an old engine that doesn't support
	// some of the additional keys
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config: %w", err)
	}
	return cfg, nil
}

func (cfg *Config) Save(w io.Writer) error {
	end := json.NewEncoder(w)
	if err := end.Encode(cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func LoadFile(fp string) (Config, error) {
	f, err := os.Open(fp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("failed to load config from %s: %w", fp, err)
	}
	defer f.Close()
	return Load(f)
}

func LoadDefault() (Config, error) {
	return LoadFile(DefaultConfigPath())
}
