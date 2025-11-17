# registry-config

Tools interacting with an OCI registry usually have their own way to authenticate.
Helm, for example, provides a command to "login" into a registry, which stores the credentials in a file.
That is, however, not a safe way to store credentials, especially not in Dagger.
Credentials persisted in the filesystem make their way into Dagger's layer cache.

This module creates a configuration file and returns it as a Secret that can be mounted safely into a Container.

## Usage

```go
type Module struct {
    // ...

	// +private
	RegistryConfig *RegistryConfig
}

func New() *Module {
	return &Module{
		// ...

		RegistryConfig: dag.RegistryConfig(),
	}
}

// use container for actions that need registry credentials
func (m *Module) container() *Container {
	return m.Container.
		With(func(c *Container) *Container {
			return m.RegistryConfig.MountSecret(c, "/root/.docker/config.json")
		})
}

// Add credentials for a registry.
func (m *Module) WithRegistryAuth(address string, username string, secret *Secret) *Module {
	m.RegistryConfig = m.RegistryConfig.WithRegistryAuth(address, username, secret)

	return m
}

// Removes credentials for a registry.
func (m *Module) WithoutRegistryAuth(address string) *Module {
	m.RegistryConfig = m.RegistryConfig.WithoutRegistryAuth(address)

	return m
}
```

## Resources

I did a presentation about this module at the Dagger Community Call on 2024-05-15.

Slides: https://slides.sagikazarmark.hu/2024-05-16-secure-registry-access-with-dagger/
Recording: coming soon

Discussion on the Dagger issue tracker: https://github.com/dagger/dagger/issues/7273
