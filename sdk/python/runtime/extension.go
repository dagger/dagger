// Helper functions for extension modules.
//
// Exension modules are runtime modules that depend on this one, to be used
// as a custom SDK.
//
// WARNING: Extending this module is considered experimental and may change
// in the future. The public API is the ModuleRuntime and Codegen functions.
package main

import "python-sdk/internal/dagger"

// Disable the discovery of custom configuration
//
// If it's not necessary, it's faster without it.
func (m *PythonSdk) WithoutUserConfig() *PythonSdk {
	m.Discovery.EnableCustomConfig = false
	return m
}

// Replace the underlying container
//
// Since all steps change this container, it's possible to extract it in one
// step, change it, and then set it with this function. Can be useful, for
// example, to add system packages between the WithBase() and WithSource()
// steps.
func (m *PythonSdk) WithContainer(
	// The container to use
	ctr *dagger.Container,
) *PythonSdk {
	m.Container = ctr
	return m
}

// Image reference for the base container
func (m *PythonSdk) BaseImage() string {
	return m.getImage(BaseImageName).String()
}

// Image reference where uv is fetched from
func (m *PythonSdk) UvImage() string {
	return m.getImage(UvImageName).String()
}

// Override the base container's image
//
// Needs to be called before Load.
func (m *PythonSdk) WithBaseImage(
	// The image reference
	ref string,
) (*PythonSdk, error) {
	m.Discovery.UserConfig().BaseImage = ref
	img, err := m.Discovery.parseBaseImage(m.Discovery.DefaultImages[BaseImageName])
	if err != nil {
		return nil, err
	}
	m.Discovery.Images[BaseImageName] = img
	return m, nil
}

// Override the uv version
//
// Needs to be called before Load. Enables uv if not already enabled.
func (m *PythonSdk) WithUvVersion(
	// The uv version
	version string,
) (*PythonSdk, error) {
	m.WithUv().Discovery.UserConfig().UvVersion = version
	img, err := m.Discovery.parseUvImage(m.Discovery.DefaultImages[UvImageName])
	if err != nil {
		return nil, err
	}
	m.Discovery.Images[UvImageName] = img
	return m, nil
}

// Check whether to use uv or not
func (m *PythonSdk) UseUv() bool {
	return m.Discovery.UserConfig().UseUv
}

// Enable the use of uv
func (m *PythonSdk) WithUv() *PythonSdk {
	m.Discovery.UserConfig().UseUv = true
	return m
}

// Disable the use of uv
func (m *PythonSdk) WithoutUv() *PythonSdk {
	m.Discovery.UserConfig().UseUv = false
	return m
}

// Version to use for uv
func (m *PythonSdk) UvVersion() string {
	return m.Discovery.UserConfig().UvVersion
}

// Uv's default index URL setting
func (m *PythonSdk) IndexURL() string {
	for _, cfg := range m.Discovery.UvConfig().Index {
		if cfg.Name != "" {
			continue
		}
		if cfg.Default {
			return cfg.URL
		}
	}
	return ""
}

// Uv's "extra-index-url" setting
func (m *PythonSdk) ExtraIndexURL() string {
	for _, cfg := range m.Discovery.UvConfig().Index {
		if cfg.Name != "" {
			continue
		}
		if !cfg.Default {
			return cfg.URL
		}
	}
	return ""
}
