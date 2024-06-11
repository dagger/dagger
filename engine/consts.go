package engine

import (
	"fmt"
	"os"
)

const (
	EngineImageRepo = "registry.dagger.io/engine"
	Package         = "github.com/dagger/dagger"

	DaggerNameEnv = "_EXPERIMENTAL_DAGGER_ENGINE_NAME"

	DaggerVersionEnv        = "_EXPERIMENTAL_DAGGER_VERSION"
	DaggerMinimumVersionEnv = "_EXPERIMENTAL_DAGGER_MIN_VERSION"

	GPUSupportEnv = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
	RunnerHostEnv = "_EXPERIMENTAL_DAGGER_RUNNER_HOST"
)

func RunnerHost() (string, error) {
	if v, ok := os.LookupEnv(RunnerHostEnv); ok {
		return v, nil
	}

	tag := Version
	if os.Getenv(GPUSupportEnv) != "" {
		tag += "-gpu"
	}
	return fmt.Sprintf("docker-image://%s:%s", EngineImageRepo, tag), nil
}

const (
	StdinPrefix  = "\x00,"
	StdoutPrefix = "\x01,"
	StderrPrefix = "\x02,"
	ResizePrefix = "resize,"
	ExitPrefix   = "exit,"
)

const (
	HTTPProxyEnvName  = "HTTP_PROXY"
	HTTPSProxyEnvName = "HTTPS_PROXY"
	FTPProxyEnvName   = "FTP_PROXY"
	NoProxyEnvName    = "NO_PROXY"
	AllProxyEnvName   = "ALL_PROXY"
)

var ProxyEnvNames = []string{
	HTTPProxyEnvName,
	HTTPSProxyEnvName,
	FTPProxyEnvName,
	NoProxyEnvName,
	AllProxyEnvName,
}
