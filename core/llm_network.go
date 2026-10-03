package core

import (
	"net/http"

	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
)

// Count encoded response bytes before the provider SDK reads the response.
// The shared transport keeps connection pooling across requests.
func newLLMNetworkClient() *http.Client {
	return &http.Client{
		Transport: enginetelemetry.NetworkResponseTransport(http.DefaultTransport, enginetelemetry.RecordNetworkRX),
	}
}
