package drivers

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/client/imageload"
)

type stubDriver struct {
	available bool
	err       error
}

func (d stubDriver) Available(context.Context, *url.URL) (bool, error) { return d.available, d.err }

func (stubDriver) Provision(context.Context, *url.URL, *DriverOpts) (Connector, error) {
	return nil, nil
}

func (stubDriver) ImageLoader(context.Context) imageload.Backend { return nil }

// registerForTest registers drivers for a scheme until the test ends, so
// that the registry still matches the scheme catalog.
func registerForTest(t *testing.T, scheme string, driver ...Driver) {
	t.Helper()
	register(scheme, driver...)
	t.Cleanup(func() { delete(drivers, scheme) })
}

func TestGetDriverAvailability(t *testing.T) {
	parse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return u
	}

	t.Run("skips unavailable drivers and returns the first usable one", func(t *testing.T) {
		usable := stubDriver{available: true}
		registerForTest(t, "getdriver-test-a",
			stubDriver{err: &UnavailableError{Reason: "first: nope"}},
			stubDriver{},
			usable,
		)

		got, err := GetDriver(t.Context(), parse("getdriver-test-a://target"))
		require.NoError(t, err)
		require.Equal(t, usable, got)
	})

	t.Run("reports why every driver was unavailable", func(t *testing.T) {
		registerForTest(t, "getdriver-test-b",
			stubDriver{err: &UnavailableError{Reason: "first: container not found"}},
			stubDriver{err: &UnavailableError{Reason: "second: cluster unreachable"}},
		)

		_, err := GetDriver(t.Context(), parse("getdriver-test-b://target?secret=value"))
		require.ErrorContains(t, err, "first: container not found")
		require.ErrorContains(t, err, "second: cluster unreachable")
		require.NotContains(t, err.Error(), "secret")
	})

	t.Run("other errors stop the search", func(t *testing.T) {
		boom := errors.New("boom")
		registerForTest(t, "getdriver-test-c", stubDriver{err: boom}, stubDriver{available: true})

		_, err := GetDriver(t.Context(), parse("getdriver-test-c://target"))
		require.ErrorIs(t, err, boom)
	})
}
