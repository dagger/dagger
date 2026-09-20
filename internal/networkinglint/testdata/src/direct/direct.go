package direct

import (
	"context"
	"net"
	"net/http"
)

func connect(ctx context.Context) {
	_, _ = net.Dial("tcp", "example.com:80")                         // want "net.Dial must use a realm"
	_, _ = (&net.Dialer{}).DialContext(ctx, "tcp", "example.com:80") // want "net.Dialer.DialContext must use realm.Dialer"
	_, _ = net.Listen("tcp", "127.0.0.1:0")                          // want "net.Listen must use a realm"
	_, _ = http.DefaultClient.Get("https://example.com")             // want "http.DefaultClient must use a realm HTTP client"
	_ = http.Client{}                                                // want "http.Client must set a realm Transport"
	_ = http.Client{Transport: http.DefaultTransport}                // want "http.Client Transport must use a realm transport"
	_, _ = net.Dial("unix", "/tmp/example.sock")
}
