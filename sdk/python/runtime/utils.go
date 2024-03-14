// Utility functions, to use in With()
package main

import "strings"

// cacheVolume returns a CacheVolume with a common prefix.
func (m *PythonSdk) cacheVolume(name string) *CacheVolume {
	image, _, _ := strings.Cut(m.Discovery.UserConfig().BaseImage, "@")
	return dag.CacheVolume(strings.Join([]string{"modpython", name, image}, "-"))
}

// install adds an install command to the container
func (m *PythonSdk) install(args ...string) func(*Container) *Container {
	return func(ctr *Container) *Container {
		var cmd []string
		// uv has a compatible api with pip
		if m.UseUv() {
			cmd = []string{"uv"}
		}
		cmd = append(cmd, "pip", "install", "--compile")
		// If there's a lock file, we assume that all the dependencies are
		// included in it so we can avoid resolving for them to get a faster
		// install.
		if m.Discovery.HasFile(LockFilePath) {
			cmd = append(cmd, "--no-deps")
		}
		return ctr.WithExec(append(cmd, args...))
	}
}
