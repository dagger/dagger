package engine

const (
	StdinPrefix  = "\x00,"
	StdoutPrefix = "\x01,"
	StderrPrefix = "\x02,"
	ResizePrefix = "resize,"
	ExitPrefix   = "exit,"

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
