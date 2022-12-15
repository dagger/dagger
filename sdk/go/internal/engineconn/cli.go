package engineconn

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrg/xdg"
)

const (
	daggerCLIBinPrefix = "dagger-"
)

var (
	// Only modified by tests, not changeable by outside users due to being in
	// an internal package
	DefaultCLIHost   = "dl.dagger.io"
	DefaultCLIScheme = "https"
)

func FromLocalCLI(ctx context.Context, cfg *Config) (EngineConn, bool, error) {
	binPath, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	if !ok {
		return nil, false, nil
	}

	binPath, err := exec.LookPath(binPath)
	if err != nil {
		return nil, false, err
	}

	conn, err := startCLISession(ctx, binPath, cfg)
	if err != nil {
		return nil, false, err
	}
	return conn, true, nil
}

func FromDownloadedCLI(ctx context.Context, cfg *Config) (EngineConn, error) {
	cacheDir := filepath.Join(xdg.CacheHome, "dagger")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}

	binName := daggerCLIBinPrefix + CLIVersion
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(cacheDir, binName)

	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		tmpbin, err := os.CreateTemp(cacheDir, "temp-"+binName)
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}
		defer tmpbin.Close()
		defer os.Remove(tmpbin.Name())

		// extract the CLI from the archive and verify it has the expected checksum
		expected, err := expectedChecksum(ctx)
		if err != nil {
			return nil, err
		}

		actual, err := extractCLI(ctx, tmpbin)
		if err != nil {
			return nil, err
		}

		if actual != expected {
			return nil, fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
		}

		// make the temp file executable and move it to its final name
		if err := tmpbin.Chmod(0o700); err != nil {
			return nil, err
		}

		if err := tmpbin.Close(); err != nil {
			return nil, fmt.Errorf("failed to close temporary file: %w", err)
		}

		if err := os.Rename(tmpbin.Name(), binPath); err != nil {
			return nil, fmt.Errorf("failed to rename %q to %q: %w", tmpbin.Name(), binPath, err)
		}

		// cleanup any old CLI binaries
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			if cfg.LogOutput != nil {
				fmt.Fprintf(cfg.LogOutput, "failed to list cache dir: %v", err)
			}
		} else {
			for _, entry := range entries {
				if entry.Name() == binName {
					continue
				}
				if strings.HasPrefix(entry.Name(), daggerCLIBinPrefix) {
					if err := os.Remove(filepath.Join(cacheDir, entry.Name())); err != nil {
						if cfg.LogOutput != nil {
							fmt.Fprintf(cfg.LogOutput, "failed to remove old dagger bin: %v", err)
						}
					}
				}
			}
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to stat %q: %w", binPath, err)
	}

	return startCLISession(ctx, binPath, cfg)
}

// returns a map of CLI archive name -> checksum for that archive
func checksumMap(ctx context.Context) (map[string]string, error) {
	checksums := make(map[string]string)

	checksumFileContents := bytes.NewBuffer(nil)
	checksumReq, err := http.NewRequestWithContext(ctx, http.MethodGet, defaultChecksumsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create checksums request: %w", err)
	}
	resp, err := http.DefaultClient.Do(checksumReq)
	if err != nil {
		return nil, fmt.Errorf("failed to download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download checksums: %s", resp.Status)
	}
	if _, err := io.Copy(checksumFileContents, resp.Body); err != nil {
		return nil, fmt.Errorf("failed to download checksums: %w", err)
	}

	scanner := bufio.NewScanner(checksumFileContents)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid checksum line: %s", line)
		}
		checksums[parts[1]] = parts[0]
	}

	return checksums, nil
}

func expectedChecksum(ctx context.Context) (string, error) {
	checksums, err := checksumMap(ctx)
	if err != nil {
		return "", err
	}

	expected, ok := checksums[defaultCLIArchiveName()]
	if !ok {
		return "", fmt.Errorf("no checksum for %s", defaultCLIArchiveName())
	}
	return expected, nil
}

// Download the CLI archive and extract the CLI from it into the provided dest.
// Returns the sha256 hash of the whole archive as read during download.
func extractCLI(ctx context.Context, dest io.Writer) (string, error) {
	archiveReq, err := http.NewRequestWithContext(ctx, http.MethodGet, defaultCLIArchiveURL(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create archive request: %w", err)
	}
	resp, err := http.DefaultClient.Do(archiveReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download CLI archive: %s", resp.Status)
	}

	// the body is a tar.gz file, untar it and extract the dagger binary
	hasher := sha256.New()
	gzipReader, err := gzip.NewReader(io.TeeReader(resp.Body, hasher))
	if err != nil {
		return "", err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var found bool
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(header.Name) == "dagger" {
			// limit the amount of data to prevent a decompression bomb (gosec G110)
			if _, err := io.CopyN(dest, tarReader, 1024*1024*1024); err != nil && err != io.EOF {
				return "", err
			}
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("failed to find dagger binary in tar.gz")
	}
	_, err = io.ReadAll(gzipReader) // ensure the entire body is read into the hash
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func defaultCLIArchiveName() string {
	// TODO:(sipsma) fix this for windows
	return fmt.Sprintf("dagger_v%s_%s_%s.tar.gz",
		CLIVersion,
		runtime.GOOS,
		runtime.GOARCH,
	)
}

func defaultCLIArchiveURL() string {
	return fmt.Sprintf("%s://%s/dagger/releases/%s/%s",
		DefaultCLIScheme,
		DefaultCLIHost,
		CLIVersion,
		defaultCLIArchiveName(),
	)
}

func defaultChecksumsURL() string {
	return fmt.Sprintf("%s://%s/dagger/releases/%s/checksums.txt",
		DefaultCLIScheme,
		DefaultCLIHost,
		CLIVersion,
	)
}
