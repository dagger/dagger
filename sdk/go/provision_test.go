package dagger

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"dagger.io/dagger/internal/engineconn"
	"github.com/adrg/xdg"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestProvision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpdir := t.TempDir()
	origCacheHome, cacheHomeSet := os.LookupEnv("XDG_CACHE_HOME")
	if cacheHomeSet {
		defer os.Setenv("XDG_CACHE_HOME", origCacheHome)
	} else {
		defer os.Unsetenv("XDG_CACHE_HOME")
	}
	os.Setenv("XDG_CACHE_HOME", tmpdir)
	xdg.Reload()
	cacheDir := filepath.Join(tmpdir, "dagger")

	// ignore DAGGER_SESSION_URL
	origSessionURL, sessionURLSet := os.LookupEnv("DAGGER_SESSION_URL")
	if sessionURLSet {
		defer os.Setenv("DAGGER_SESSION_URL", origSessionURL)
	}
	os.Unsetenv("DAGGER_SESSION_URL")

	// Setup a test server if _EXPERIMENTAL_DAGGER_CLI_BIN is set
	binPath, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	if ok {
		defer os.Setenv("_EXPERIMENTAL_DAGGER_CLI_BIN", binPath)
		os.Unsetenv("_EXPERIMENTAL_DAGGER_CLI_BIN")

		originalBaseURL := engineconn.DefaultCLIHost
		defer func() {
			engineconn.DefaultCLIHost = originalBaseURL
		}()
		originalScheme := engineconn.DefaultCLIScheme
		defer func() {
			engineconn.DefaultCLIScheme = originalScheme
		}()
		engineconn.DefaultCLIScheme = "http"

		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer l.Close()
		engineconn.DefaultCLIHost = l.Addr().String()

		basePath := fmt.Sprintf("dagger/releases/%s/", engineconn.CLIVersion)

		archiveBytes := createCLIArchive(t, binPath)
		archiveName := fmt.Sprintf("dagger_v%s_%s_%s.tar.gz", engineconn.CLIVersion, runtime.GOOS, runtime.GOARCH)
		archivePath := path.Join(basePath, archiveName)

		checksum := sha256.Sum256(archiveBytes.Bytes())
		checksumFileContents := fmt.Sprintf("%x  %s\n", checksum, archiveName)
		checksumPath := path.Join(basePath, "checksums.txt")

		go http.Serve(l, http.FileServer(http.FS(fstest.MapFS{
			checksumPath: &fstest.MapFile{
				Data:    []byte(checksumFileContents),
				Mode:    0o644,
				ModTime: time.Now(),
			},
			archivePath: &fstest.MapFile{
				Data:    archiveBytes.Bytes(),
				Mode:    0o755,
				ModTime: time.Now(),
			},
		})))
	}

	// create some garbage for the image provisioner to collect
	err := os.MkdirAll(cacheDir, 0700)
	require.NoError(t, err)
	f, err := os.Create(filepath.Join(cacheDir, "dagger-0.0.0"))
	require.NoError(t, err)
	f.Close()

	parallelism := runtime.NumCPU()
	start := make(chan struct{})
	var eg errgroup.Group
	for i := 0; i < parallelism; i++ {
		eg.Go(func() error {
			<-start
			c, err := Connect(ctx, WithLogOutput(os.Stderr))
			if err != nil {
				return fmt.Errorf("failed to connect: %w", err)
			}
			defer c.Close()
			// do a trivial query to ensure the engine is actually there
			_, err = c.DefaultPlatform(ctx)
			if err != nil {
				return fmt.Errorf("failed to query: %w", err)
			}
			return nil
		})
	}
	close(start)
	require.NoError(t, eg.Wait())

	entries, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]
	require.True(t, entry.Type().IsRegular())
	require.True(t, strings.HasPrefix(entry.Name(), "dagger-"))
}

func createCLIArchive(t *testing.T, binPath string) *bytes.Buffer {
	t.Helper()

	buf := bytes.NewBuffer(nil)
	gzw := gzip.NewWriter(buf)
	defer gzw.Close()
	tw := tar.NewWriter(gzw)
	defer tw.Close()

	f, err := os.Open(binPath)
	require.NoError(t, err)
	defer f.Close()
	stat, err := f.Stat()
	require.NoError(t, err)
	hdr, err := tar.FileInfoHeader(stat, "")
	require.NoError(t, err)
	hdr.Name = "dagger"
	require.NoError(t, tw.WriteHeader(hdr))
	_, err = io.Copy(tw, f)
	require.NoError(t, err)

	return buf
}
