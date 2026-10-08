// telemetry-session runs one Dagger session for the telemetry tests, in the
// trace of $TRACEPARENT when it is set: it runs `echo <marker>` in a container
// from $IMAGE. Then it creates the file $DONE, when set, and keeps the session
// open until the file $AWAIT exists, when set, for at most two minutes, making
// no further call.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

func main() {
	if err := run(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, marker string) error {
	c, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	return errors.Join(session(ctx, c, marker), c.Close())
}

func session(ctx context.Context, c *dagger.Client, marker string) error {
	if _, err := core.NewQuery(c).Container().From(os.Getenv("IMAGE")).WithExec([]string{"echo", marker}).Sync(ctx); err != nil {
		return err
	}
	if done := os.Getenv("DONE"); done != "" {
		if err := os.WriteFile(done, nil, 0o644); err != nil {
			return err
		}
	}
	await := os.Getenv("AWAIT")
	if await == "" {
		return nil
	}
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := os.Stat(await); err == nil {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for %s", await)
}
