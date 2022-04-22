package mod

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	securejoin "github.com/cyphar/filepath-securejoin"
)

func download(ctx context.Context, require *Require, dir string, auth string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", require.cloneRepo, nil)
	if err != nil {
		return fmt.Errorf("error downloading %s: %w", require.cloneRepo, err)
	}

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error downloading %s: %w", require.cloneRepo, err)
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		return fmt.Errorf("error downloading %s: status code %d", require.cloneRepo, response.StatusCode)
	}

	stream, err := gzip.NewReader(response.Body)
	if err != nil {
		return err
	}

	return extractTar(stream, dir)
}

func extractTar(stream io.Reader, dir string) error {
	tarReader := tar.NewReader(stream)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
			continue
		}

		path, err := securejoin.SecureJoin(dir, header.Name)
		if err != nil {
			return err
		}

		info := header.FileInfo()
		if info.IsDir() {
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				return err
			}
			continue
		}

		parent := filepath.Dir(path)
		if err = os.MkdirAll(parent, 0o777); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.CopyN(file, tarReader, 100*1024*1024)
		if err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}
