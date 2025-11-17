// Create an OCI registry configuration file and use it safely with tools, like Helm or Oras.
//
// Tools interacting with an OCI registry usually have their own way to authenticate.
// Helm, for example, provides a command to "login" into a registry, which stores the credentials in a file.
// That is, however, not a safe way to store credentials, especially not in Dagger.
// Credentials persisted in the filesystem make their way into Dagger's layer cache.
//
// This module creates a configuration file and returns it as a Secret that can be mounted safely into a Container.
//
// Be advised that using the tool's built-in authentication mechanism may not work with the configuration file (since it's read only).
//
// You can read more about the topic in the readme: https://github.com/sagikazarmark/daggerverse/tree/main/registry-config#resources
package main

import (
	"context"
	"dagger/registry-config/internal/dagger"
	"slices"
)

type RegistryConfig struct {
	// +private
	Auths []Auth
}

type Auth struct {
	Address  string
	Username string
	Secret   *dagger.Secret
}

// Add credentials for a registry.
func (m *RegistryConfig) WithRegistryAuth(address string, username string, secret *dagger.Secret) *RegistryConfig {
	m.Auths = append(m.Auths, Auth{
		Address:  address,
		Username: username,
		Secret:   secret,
	})

	return m
}

// Removes credentials for a registry.
func (m *RegistryConfig) WithoutRegistryAuth(address string) *RegistryConfig {
	m.Auths = slices.DeleteFunc(m.Auths, func(a Auth) bool {
		return a.Address == address
	})

	return m
}

// Create the registry configuration.
func (m *RegistryConfig) Secret(
	ctx context.Context,

	// Customize the name of the secret.
	//
	// +optional
	name string,
) (*dagger.Secret, error) {
	config, err := m.toConfig(ctx)
	if err != nil {
		return nil, err
	}

	return config.toSecret(name)
}

// Create a SecretMount that can be used to mount the registry configuration into a container.
func (m *RegistryConfig) SecretMount(
	ctx context.Context,

	// Path to mount the secret into (a common path is ~/.docker/config.json).
	path string,

	// Name of the secret to create and mount.
	//
	// +optional
	secretName string,

	// Skip mounting the secret if it's empty.
	//
	// +optional
	skipOnEmpty bool,

	// A user:group to set for the mounted secret.
	//
	// The user and group can either be an ID (1000:1000) or a name (foo:bar).
	//
	// If the group is omitted, it defaults to the same as the user.
	//
	// +optional
	owner string,

	// Permission given to the mounted secret (e.g., 0600).
	//
	// This option requires an owner to be set to be active.
	//
	// +optional
	mode int,
) *SecretMount {
	return &SecretMount{
		Path:           path,
		SecretName:     secretName,
		SkipOnEmpty:    skipOnEmpty,
		Owner:          owner,
		Mode:           mode,
		RegistryConfig: m,
	}
}

type SecretMount struct {
	// Path to mount the secret into (a common path is ~/.docker/config.json).
	Path string

	// Name of the secret to create and mount.
	SecretName string

	// Skip mounting the secret if it's empty.
	SkipOnEmpty bool

	// A user:group to set for the mounted secret.
	Owner string

	// Permission given to the mounted secret (e.g., 0600).
	Mode int

	// DO NOT USE
	// Made public until https://github.com/dagger/dagger/pull/8149 is fixed.
	// private
	RegistryConfig *RegistryConfig
}

func (m *SecretMount) Mount(ctx context.Context, container *dagger.Container) (*dagger.Container, error) {
	if m.SkipOnEmpty && len(m.RegistryConfig.Auths) == 0 {
		return container, nil
	}

	secret, err := m.RegistryConfig.Secret(ctx, m.SecretName)
	if err != nil {
		return nil, err
	}

	return container.WithMountedSecret(m.Path, secret, dagger.ContainerWithMountedSecretOpts{
		Owner: m.Owner,
		Mode:  m.Mode,
	}), nil
}
