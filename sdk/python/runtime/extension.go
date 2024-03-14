// Helper functions for extension modules.
//
// Exension modules are runtime modules that depend on this one, to be used
// as a custom SDK.
//
// WARNING: Extending this module is considered experimental and may change
// in the future. The public API is the ModuleRuntime and Codegen functions.
package main

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
func (m *PythonSdk) WithContainer(c *Container) *PythonSdk {
	m.Container = c
	return m
}

// Image reference for the base image
func (m *PythonSdk) BaseImage() string {
	return m.Discovery.UserConfig().BaseImage
}

// Override the base image reference
func (m *PythonSdk) WithBaseImage(image string) *PythonSdk {
	m.Discovery.UserConfig().BaseImage = image
	return m
}

// Check wheter to use uv or not
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
