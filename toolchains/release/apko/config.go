package main

import "dagger/apko/internal/dagger"

// Load the Wolfi base configuration.
func (m *Apko) Wolfi() *Config {
	return m.Preset("wolfi-base")
}

// Load the Alpine base configuration.
func (m *Apko) Alpine() *Config {
	return m.Preset("alpine-base")
}

// Load a configuration preset.
func (m *Apko) Preset(name string) *Config {
	return &Config{
		File: dag.CurrentModule().Source().File("presets/" + name + ".yaml"),
		Apko: m,
	}
}

// Load a configuration file.
func (m *Apko) Config(file *dagger.File) *Config {
	return &Config{
		File: file,
		Apko: m,
	}
}

type Config struct {
	File *dagger.File

	Repositories []string
	Keyrings     []string
	Archs        []string
	Packages     []string

	// +private
	Apko *Apko
}

// Add a repository to the configuration.
func (m *Config) WithRepository(url string) *Config {
	m.Repositories = append(m.Repositories, url)

	return m
}

// Add a keyring to the configuration.
func (m *Config) WithKeyring(url string) *Config {
	m.Keyrings = append(m.Keyrings, url)

	return m
}

// Add an arch to the configuration.
func (m *Config) WithArch(arch string) *Config {
	m.Archs = append(m.Archs, arch)

	return m
}

// Add a package to the configuration.
func (m *Config) WithPackage(name string) *Config {
	m.Packages = append(m.Packages, name)

	return m
}

// Add a list of packages to the configuration.
func (m *Config) WithPackages(pkgs []string) *Config {
	m.Packages = append(m.Packages, pkgs...)

	return m
}

// Build an image from configuration.
func (m *Config) Build(tag string) *BuildResult {
	return m.Apko.Build(m.File, tag, nil, nil, m.Archs, "", m.Keyrings, false, m.Packages, m.Repositories, true)
}

// Build a container from configuration.
func (m *Config) Container() *dagger.Container {
	return m.Build("latest").AsContainer()
}
