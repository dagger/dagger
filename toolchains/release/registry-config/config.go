package main

import (
	"context"
	"crypto/sha1"
	"dagger/registry-config/internal/dagger"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type Config struct {
	Auths map[string]ConfigAuth `json:"auths"`
}

type ConfigAuth struct {
	Auth string `json:"auth"`
}

func (m *RegistryConfig) toConfig(ctx context.Context) (*Config, error) {
	config := &Config{
		Auths: map[string]ConfigAuth{},
	}

	for _, auth := range m.Auths {
		plaintext, err := auth.Secret.Plaintext(ctx)
		if err != nil {
			return nil, err
		}

		config.Auths[auth.Address] = ConfigAuth{
			Auth: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", auth.Username, plaintext))),
		}
	}

	return config, nil
}

func (c *Config) toSecret(name string) (*dagger.Secret, error) {
	out, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}

	if name == "" {
		h := sha1.New()

		_, err := h.Write(out)
		if err != nil {
			return nil, err
		}

		name = fmt.Sprintf("registry-config-%x", h.Sum(nil))
	}

	return dag.SetSecret(name, string(out)), nil
}
