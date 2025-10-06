package engine

import "github.com/dagger/dagger/engine/distconsts"

const (
	EngineImageRepo = "registry.dagger.io/engine"
	Package         = "github.com/dagger/dagger"

	DefaultEngineSockAddr = distconsts.DefaultEngineSockAddr

	DaggerNameEnv = "_EXPERIMENTAL_DAGGER_ENGINE_NAME"

	DaggerVersionEnv        = "_EXPERIMENTAL_DAGGER_VERSION"
	DaggerMinimumVersionEnv = "_EXPERIMENTAL_DAGGER_MIN_VERSION"
)

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

const (
	OTelTraceParentEnv      = "TRACEPARENT"
	OTelExporterProtocolEnv = "OTEL_EXPORTER_OTLP_PROTOCOL"
	OTelExporterEndpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"
	OTelTracesProtocolEnv   = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	OTelTracesEndpointEnv   = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	OTelTracesLiveEnv       = "OTEL_EXPORTER_OTLP_TRACES_LIVE"
	OTelLogsProtocolEnv     = "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"
	OTelLogsEndpointEnv     = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	OTelMetricsProtocolEnv  = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"
	OTelMetricsEndpointEnv  = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
)

var ProxyEnvNames = []string{
	HTTPProxyEnvName,
	HTTPSProxyEnvName,
	FTPProxyEnvName,
	NoProxyEnvName,
	AllProxyEnvName,
}
