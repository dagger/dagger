package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dagger.io/dagger"
)

// Give a clean shutdown time to write its persistence checkpoint under load.
// Each step gets this full bound, independent of earlier steps and of test
// cancellation; a failed step must not prevent the remaining cleanup.
const fixtureEngineStopTimeout = 2 * time.Minute

// closeClientBounded joins Close, which takes no context, under a fresh
// deadline. The buffered result lets the closer finish after a timeout.
func closeClientBounded(ctx context.Context, client *dagger.Client) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureEngineStopTimeout)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			return fmt.Errorf("client close: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("client close did not return: %w", context.Cause(ctx))
	}
}

// stopNestedEngine attempts every shutdown step under its own deadline and
// reports each failed step by name. Services are forgotten only after a
// successful stop, so cleanup can retry; a client's Close is called only once.
func stopNestedEngine(ctx context.Context, client **dagger.Client, upstream, tunnel **dagger.Service) error {
	step := func(name string, run func(context.Context) error) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureEngineStopTimeout)
		defer cancel()
		if err := run(ctx); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	var errs error
	if client != nil && *client != nil {
		c := *client
		*client = nil
		errs = errors.Join(errs, closeClientBounded(ctx, c))
	}
	if upstream != nil && *upstream != nil {
		svc := *upstream
		err := step("nested engine stop", func(ctx context.Context) error {
			_, err := svc.Stop(ctx)
			return err
		})
		if err == nil {
			*upstream = nil
		}
		errs = errors.Join(errs, err)
	}
	if tunnel != nil && *tunnel != nil {
		svc := *tunnel
		err := step("nested tunnel stop", func(ctx context.Context) error {
			_, err := svc.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
			return err
		})
		if err == nil {
			*tunnel = nil
		}
		errs = errors.Join(errs, err)
	}
	return errs
}
