package engineconn

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrg/xdg"
	"github.com/mitchellh/go-homedir"
)

const (
	daggerCLIBinPrefix = "dagger-"
	defaultCLIHost     = "dl.dagger.io"
	windowsPlatform    = "windows"
)

var (
	errCLIReleaseUnavailable = errors.New("CLI release unavailable")

	// Only modified by tests, not changeable by outside users due to being in
	// an internal package
	OverrideCLIArchiveURL string
	OverrideChecksumsURL  string
)

type CLIDownloader struct {
	Ref        string
	Release    bool
	LogOutput  io.Writer
	CleanupOld bool
}

func FromLocalCLI(ctx context.Context, cfg *Config) (EngineConn, bool, error) {
	binPath, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	if !ok {
		return nil, false, nil
	}
	binPath, err := homedir.Expand(binPath)
	if err != nil {
		return nil, false, err
	}
	binPath, err = exec.LookPath(binPath)
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
	binPath, downloadErr := (CLIDownloader{
		Ref:        CLIVersion,
		Release:    true,
		LogOutput:  cfg.LogOutput,
		CleanupOld: true,
	}).Download(ctx)
	if downloadErr != nil {
		var err error
		binPath, err = fallbackToLocalCLI(downloadErr, cfg.LogOutput)
		if err != nil {
			return nil, err
		}
	}

	conn, err := startCLISession(ctx, binPath, cfg)
	if err != nil && downloadErr != nil {
		return nil, errors.Join(downloadErr, fmt.Errorf("failed to use CLI from PATH %q: %w", binPath, err))
	}
	return conn, err
}

func fallbackToLocalCLI(downloadErr error, logOutput io.Writer) (string, error) {
	if !errors.Is(downloadErr, errCLIReleaseUnavailable) {
		return "", downloadErr
	}

	binPath, err := exec.LookPath("dagger")
	if err != nil {
		return "", errors.Join(downloadErr, fmt.Errorf("dagger CLI not found in PATH: %w", err))
	}
	if logOutput == nil {
		logOutput = os.Stderr
	}
	fmt.Fprintf(logOutput,
		"CLI version %s is unavailable; using %s from PATH (version compatibility is not guaranteed).\n",
		CLIVersion,
		binPath,
	)
	return binPath, nil
}

func (d CLIDownloader) Download(ctx context.Context) (string, error) {
	if d.Ref == "" {
		return "", fmt.Errorf("CLI download ref must not be empty")
	}

	cacheDir := filepath.Join(xdg.CacheHome, "dagger")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", err
	}

	binName := daggerCLIBinPrefix + d.Ref
	if runtime.GOOS == windowsPlatform {
		binName += ".exe"
	}
	binPath := filepath.Join(cacheDir, binName)

	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		tmpbin, err := os.CreateTemp(cacheDir, "temp-"+binName)
		if err != nil {
			return "", fmt.Errorf("failed to create temp file: %w", err)
		}
		defer tmpbin.Close()
		defer os.Remove(tmpbin.Name())

		var expected string
		if d.Release {
			expected, err = d.expectedChecksum(ctx)
			if err != nil {
				return "", err
			}
		}

		if d.LogOutput != nil {
			fmt.Fprintf(d.LogOutput, "Downloading CLI... ")
		}

		actual, err := d.extract(ctx, tmpbin)
		if err != nil {
			return "", err
		}

		if d.Release {
			if actual != expected {
				return "", fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
			}
		}

		// make the temp file executable and move it to its final name
		if err := tmpbin.Chmod(0o700); err != nil {
			return "", err
		}

		if err := tmpbin.Close(); err != nil {
			return "", fmt.Errorf("failed to close temporary file: %w", err)
		}

		if err := os.Rename(tmpbin.Name(), binPath); err != nil {
			return "", fmt.Errorf("failed to rename %q to %q: %w", tmpbin.Name(), binPath, err)
		}

		if d.LogOutput != nil {
			fmt.Fprintln(d.LogOutput, "OK!")
		}

		// cleanup any old CLI binaries
		if d.CleanupOld {
			cleanupOldCLIs(cacheDir, binName, d.LogOutput)
		}
	} else if err != nil {
		return "", fmt.Errorf("failed to stat %q: %w", binPath, err)
	}

	return binPath, nil
}

func cleanupOldCLIs(cacheDir, currentBinName string, logOutput io.Writer) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if logOutput != nil {
			fmt.Fprintf(logOutput, "failed to list cache dir: %v", err)
		}
		return
	}
	for _, entry := range entries {
		if entry.Name() == currentBinName {
			continue
		}
		if strings.HasPrefix(entry.Name(), daggerCLIBinPrefix) {
			if err := os.Remove(filepath.Join(cacheDir, entry.Name())); err != nil {
				if logOutput != nil {
					fmt.Fprintf(logOutput, "failed to remove old dagger bin: %v", err)
				}
			}
		}
	}
}

// returns a map of CLI archive name -> checksum for that archive
func (d CLIDownloader) checksumMap(ctx context.Context) (map[string]string, error) {
	if !d.Release {
		return nil, fmt.Errorf("checksums are only available for release CLI downloads")
	}

	checksums := make(map[string]string)
	checksumsURL := d.checksumsURL()

	checksumFileContents := bytes.NewBuffer(nil)
	checksumReq, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create checksums request: %w", err)
	}
	resp, err := http.DefaultClient.Do(checksumReq)
	if err != nil {
		return nil, fmt.Errorf("failed to download checksums from %s: %w", checksumsURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("failed to download checksums from %s: %s", checksumsURL, resp.Status)
		if isCLIReleaseUnavailable(resp.StatusCode) {
			err = fmt.Errorf("%w: %w", errCLIReleaseUnavailable, err)
		}
		return nil, err
	}
	if _, err := io.Copy(checksumFileContents, resp.Body); err != nil {
		return nil, fmt.Errorf("failed to download checksums from %s: %w", checksumsURL, err)
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
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan checksums from %s: %w", checksumsURL, err)
	}

	return checksums, nil
}

func (d CLIDownloader) expectedChecksum(ctx context.Context) (string, error) {
	checksums, err := d.checksumMap(ctx)
	if err != nil {
		return "", err
	}

	archiveName := d.archiveName()
	expected, ok := checksums[archiveName]
	if !ok {
		return "", fmt.Errorf("no checksum for %s", archiveName)
	}
	return expected, nil
}

// Download the CLI archive and extract the CLI from it into the provided dest.
// Returns the sha256 hash of the whole archive as read during download.
func (d CLIDownloader) extract(ctx context.Context, dest io.Writer) (string, error) {
	archiveURL := d.archiveURL()
	archiveReq, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create archive request: %w", err)
	}
	resp, err := http.DefaultClient.Do(archiveReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download CLI archive from %s: %s", archiveURL, resp.Status)
	}

	// the body is either a tar.gz file or (on windows) a zipfile, unpack it and extract the dagger binary
	hasher := sha256.New()
	reader := io.TeeReader(resp.Body, hasher)
	if runtime.GOOS == windowsPlatform {
		if err := extractZip(reader, dest); err != nil {
			return "", err
		}
	} else if err := extractTarCLI(reader, dest); err != nil {
		return "", err
	}

	_, err = io.ReadAll(reader) // ensure the entire body is read into the hash
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func isCLIReleaseUnavailable(statusCode int) bool {
	// dl.dagger.io returns 403 for missing S3 objects.
	return statusCode == http.StatusForbidden || statusCode == http.StatusNotFound
}

func extractTarCLI(src io.Reader, dest io.Writer) error {
	gzipReader, err := gzip.NewReader(src)
	if err != nil {
		return err
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
			return err
		}
		if filepath.Base(header.Name) == "dagger" {
			// limit the amount of data to prevent a decompression bomb (gosec G110)
			if _, err := io.CopyN(dest, tarReader, 1024*1024*1024); err != nil && err != io.EOF {
				return err
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("failed to find dagger binary in tar.gz")
	}
	return nil
}

func extractZip(src io.Reader, dest io.Writer) error {
	tmpFile, err := os.CreateTemp("", "dagger-cli-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()
	if _, err := io.Copy(tmpFile, src); err != nil {
		return err
	}
	zipReader, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return err
	}
	defer zipReader.Close()
	var found bool
	for _, file := range zipReader.File {
		if filepath.Base(file.Name) == "dagger.exe" {
			f, err := file.Open()
			if err != nil {
				return err
			}
			defer f.Close()
			// limit the amount of data to prevent a decompression bomb (gosec G110)
			if _, err := io.CopyN(dest, f, 1024*1024*1024); err != nil && err != io.EOF {
				return err
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("failed to find dagger.exe binary in zip")
	}
	return nil
}

func cliArchiveName(version string) string {
	ext := "tar.gz"
	if runtime.GOOS == windowsPlatform {
		ext = "zip"
	}
	return fmt.Sprintf("dagger_%s_%s_%s.%s",
		version,
		runtime.GOOS,
		runtime.GOARCH,
		ext,
	)
}

func (d CLIDownloader) archiveName() string {
	if OverrideCLIArchiveURL != "" {
		return archiveNameFromURL(OverrideCLIArchiveURL)
	}
	ref := d.Ref
	if d.Release {
		ref = "v" + strings.TrimPrefix(ref, "v")
	}
	return cliArchiveName(ref)
}

func archiveNameFromURL(rawURL string) string {
	url, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return filepath.Base(url.Path)
}

func (d CLIDownloader) archiveURL() string {
	if OverrideCLIArchiveURL != "" {
		return OverrideCLIArchiveURL
	}
	if d.Release {
		return fmt.Sprintf("https://%s/dagger/releases/%s/%s",
			defaultCLIHost,
			strings.TrimPrefix(d.Ref, "v"),
			d.archiveName(),
		)
	}
	return fmt.Sprintf("https://%s/dagger/main/%s/%s",
		defaultCLIHost,
		d.Ref,
		d.archiveName(),
	)
}

func (d CLIDownloader) checksumsURL() string {
	if OverrideChecksumsURL != "" {
		return OverrideChecksumsURL
	}
	return fmt.Sprintf("https://%s/dagger/releases/%s/checksums.txt",
		defaultCLIHost,
		strings.TrimPrefix(d.Ref, "v"),
	)
}
