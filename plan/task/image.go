package task

import (
	"time"
)

// ImageConfig defines the execution parameters which should be used as a base when running a container using an image.
type ImageConfig struct {
	// [Image Spec](https://github.com/opencontainers/image-spec/blob/main/specs-go/v1/config.go)

	// User defines the username or UID which the process in the container should run as.
	User string `json:"user,omitempty"`

	// ExposedPorts a set of ports to expose from a container running this image.
	ExposedPorts map[string]struct{} `json:"expose,omitempty"`

	// Env is a list of environment variables to be used in a container.
	Env map[string]string `json:"env,omitempty"`

	// Entrypoint defines a list of arguments to use as the command to execute when the container starts.
	Entrypoint []string `json:"entrypoint,omitempty"`

	// Cmd defines the default arguments to the entrypoint of the container.
	Cmd []string `json:"cmd,omitempty"`

	// Volumes is a set of directories describing where the process is likely write data specific to a container instance.
	Volumes map[string]struct{} `json:"volume,omitempty"`

	// WorkingDir sets the current working directory of the entrypoint process in the container.
	WorkingDir string `json:"workdir,omitempty"`

	// Labels contains arbitrary metadata for the container.
	Labels map[string]string `json:"label,omitempty"`

	// StopSignal contains the system call signal that will be sent to the container to exit.
	StopSignal string `json:"stopsignal,omitempty"`

	// [Docker Superset](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/dockerfile2llb/image.go)

	Healthcheck *HealthConfig `json:"healthcheck,omitempty"` // Healthcheck describes how to check the container is healthy
	ArgsEscaped bool          `json:"argsescaped,omitempty"` // True if command is already escaped (Windows specific)

	OnBuild     []string `json:"onbuild,omitempty"`     // ONBUILD metadata that were defined on the image Dockerfile
	StopTimeout *int     `json:"stoptimeout,omitempty"` // Timeout (in seconds) to stop a container
	Shell       []string `json:"shell,omitempty"`       // Shell for shell-form of RUN, CMD, ENTRYPOINT
}

// HealthConfig holds configuration settings for the HEALTHCHECK feature.
type HealthConfig struct {
	// Test is the test to perform to check that the container is healthy.
	// An empty slice means to inherit the default.
	// The options are:
	// {} : inherit healthcheck
	// {"NONE"} : disable healthcheck
	// {"CMD", args...} : exec arguments directly
	// {"CMD-SHELL", command} : run command with system's default shell
	Test []string `json:"test,omitempty"`

	// Zero means to inherit. Durations are expressed as integer nanoseconds.
	Interval    time.Duration `json:"interval,omitempty"`    // Interval is the time to wait between checks.
	Timeout     time.Duration `json:"timeout,omitempty"`     // Timeout is the time to wait before considering the check to have hung.
	StartPeriod time.Duration `json:"startperiod,omitempty"` // The start period for the container to initialize before the retries starts to count down.

	// Retries is the number of consecutive failures needed to consider a container as unhealthy.
	// Zero means inherit.
	Retries int `json:"retries,omitempty"`
}
