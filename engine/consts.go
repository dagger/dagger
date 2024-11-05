package engine

import (
	"fmt"
	"os"

	"github.com/dagger/dagger/engine/distconsts"
)

var (
	// Tag holds the tag that the respective engine version is tagged with.
	//
	// Note: this is filled at link-time.
	//
	// - For official tagged releases, this is simple semver like vX.Y.Z
	// - For untagged builds, this is a commit sha for the last known commit from main
	// - For dev builds, this is the last known commit from main (or maybe empty)
	Tag string
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

func RunnerHost() string {
	if v, ok := os.LookupEnv(RunnerHostEnv); ok {
		return v
	}

	tag := Tag
	if tag == "" {
		// can happen during naive dev builds (so just fallback to something
		// semi-reasonable)
		return "docker-container://" + distconsts.EngineContainerName
	}
	if os.Getenv(GPUSupportEnv) != "" {
		tag += "-gpu"
	}
	return fmt.Sprintf("docker-image://%s:%s", EngineImageRepo, tag)
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

	SessionAttachablesEndpoint = "/sessionAttachables"
	InitEndpoint               = "/init"
	QueryEndpoint              = "/query"
	ShutdownEndpoint           = "/shutdown"

	// Buildkit-interpreted session keys, can't change
	SessionIDMetaKey         = "X-Docker-Expose-Session-Uuid"
	SessionNameMetaKey       = "X-Docker-Expose-Session-Name"
	SessionSharedKeyMetaKey  = "X-Docker-Expose-Session-Sharedkey"
	SessionMethodNameMetaKey = "X-Docker-Expose-Session-Grpc-Method"
)

var ProxyEnvNames = []string{
	HTTPProxyEnvName,
	HTTPSProxyEnvName,
	FTPProxyEnvName,
	NoProxyEnvName,
	AllProxyEnvName,
}
