package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"dagger/caller/internal/dagger"

	"golang.org/x/sync/errgroup"
)

type Caller struct{}

func (m *Caller) Run(ctx context.Context) error {
	foo := dag.Relayer().Service().WithHostname("foo")
	bar := dag.Relayer().Service().WithHostname("bar")
	baz := dag.Relayer().Service().WithHostname("baz")

	startGroup := new(errgroup.Group)
	for _, srv := range []*dagger.Service{foo, bar, baz} {
		startGroup.Go(func() error {
			_, err := srv.Start(ctx)
			return err
		})
	}
	if err := startGroup.Wait(); err != nil {
		return err
	}

	relayURL := &url.URL{
		Scheme: "http",
		Host:   "foo",
		Path:   "/",
		RawQuery: url.Values{
			"relay": {
				"http://bar",
				"http://baz",
				"http://foo",
			},
			"end": {"hello"},
		}.Encode(),
	}

	resp, err := dag.Container().
		From("busybox:1.37.0").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"cat", "/etc/resolv.conf"}).
		WithExec([]string{"wget", "-O-", relayURL.String()}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to reach service: %w", err)
	}
	if resp != "http://bar: http://baz: http://foo: hello" {
		return fmt.Errorf("unexpected response: %q", resp)
	}

	return nil
}
