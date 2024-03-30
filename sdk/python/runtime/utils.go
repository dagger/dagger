package main

import (
	_ "embed"
	"strings"
)

//go:embed requirements.txt
var reqs string

// getRequirement returns the version constraint for a tool, saved in this
// module's requirements.txt file.
func getRequirement(name string) string {
	for _, line := range strings.Split(reqs, "\n") {
		if strings.HasPrefix(line, name) {
			return strings.TrimPrefix(line, name)
		}
	}
	return ""
}

// cacheVolume returns a CacheVolume with a common prefix.
func (m *PythonSdk) cacheVolume(name string) *CacheVolume {
	image, _, _ := strings.Cut(m.Discovery.UserConfig().BaseImage, "@")
	return dag.CacheVolume(strings.Join([]string{"modpython", name, image}, "-"))
}

// install adds an install command to the container
func (m *PythonSdk) install(args ...string) func(*Container) *Container {
	return func(ctr *Container) *Container {
		cmd := []string{"pip", "install", "--compile"}
		// uv has a compatible api with pip
		if m.UseUv() {
			cmd = append([]string{"uv"}, append(cmd, "--strict")...)
		}
		// If there's a lock file, we assume that all the dependencies are
		// included in it so we can avoid resolving for them to get a faster
		// install.
		if m.Discovery.HasFile(LockFilePath) {
			cmd = append(cmd, "--no-deps")
		}
		return ctr.WithExec(append(cmd, args...))
	}
}
