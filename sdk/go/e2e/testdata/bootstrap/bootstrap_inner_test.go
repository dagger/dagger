package bootstrap_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/engineconn"
)

func TestBootstrap(t *testing.T) {
	if err := run(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	archiveURL := os.Getenv("_INTERNAL_DAGGER_TEST_CLI_URL")
	checksumsURL := os.Getenv("_INTERNAL_DAGGER_TEST_CLI_CHECKSUMS_URL")
	if archiveURL == "" || checksumsURL == "" {
		return fmt.Errorf("bootstrap fixture URLs are required")
	}

	// These overrides are the intentional injection boundary: the development
	// library still performs its real download, checksum, extraction, cache, and
	// CLI execution path without depending on release storage.
	engineconn.OverrideCLIArchiveURL = archiveURL
	engineconn.OverrideChecksumsURL = checksumsURL
	defer func() {
		engineconn.OverrideCLIArchiveURL = ""
		engineconn.OverrideChecksumsURL = ""
	}()

	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		return fmt.Errorf("XDG_CACHE_HOME is required")
	}
	cacheDir := filepath.Join(cacheHome, "dagger")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "dagger-0.0.0"), nil, 0o600); err != nil {
		return fmt.Errorf("create stale cache entry: %w", err)
	}

	connect := func(name string) (rerr error) {
		fmt.Fprintf(os.Stderr, "%s: connecting\n", name)
		client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
		if err != nil {
			return fmt.Errorf("%s: connect: %w", name, err)
		}
		defer func() {
			if err := client.Close(); err != nil && rerr == nil {
				rerr = fmt.Errorf("%s: close: %w", name, err)
			}
		}()

		if _, err := client.DefaultPlatform(ctx); err != nil {
			return fmt.Errorf("%s: query default platform: %w", name, err)
		}
		fmt.Fprintf(os.Stderr, "%s: connected and queried\n", name)
		return nil
	}

	// Complete the initial download before exercising concurrent reuse of the
	// downloaded CLI and its cache coordination.
	if err := connect("initial"); err != nil {
		return err
	}

	parallelism := runtime.NumCPU()
	start := make(chan struct{})
	errs := make(chan error, parallelism)
	var connects sync.WaitGroup
	for i := range parallelism {
		connects.Go(func() {
			<-start
			errs <- connect(fmt.Sprintf("concurrent-%d", i))
		})
	}
	close(start)
	connects.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return fmt.Errorf("read cache directory: %w", err)
	}
	var cliEntries []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "dagger-") {
			cliEntries = append(cliEntries, entry.Name())
		}
	}
	if len(cliEntries) != 1 {
		return fmt.Errorf("expected exactly one cached CLI after garbage collection, found %v", cliEntries)
	}
	return nil
}
