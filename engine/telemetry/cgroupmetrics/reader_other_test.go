//go:build !linux

package cgroupmetrics

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
)

func TestRegisterReturnsNoopRegistration(t *testing.T) {
	registration, err := Register(context.Background(), noop.NewMeterProvider().Meter(InstrumentationScopeName))
	if err != nil {
		t.Fatal(err)
	}
	if registration == nil {
		t.Fatal("registration is nil")
	}
	if err := registration.Unregister(); err != nil {
		t.Fatal(err)
	}
}
