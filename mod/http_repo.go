package mod

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/rs/zerolog/log"
)

func download(ctx context.Context, url string, dir string, auth string, removeFirst bool) error {
	lg := log.Ctx(ctx)
	lg.Debug().Msgf("downloading %s...", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("error downloading %s: %w", url, err)
	}

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error downloading %s: %w", url, err)
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		return fmt.Errorf("error downloading %s: status code %d", url, response.StatusCode)
	}

	stream, err := gzip.NewReader(response.Body)
	if err != nil {
		return err
	}

	return extractTar(stream, dir, removeFirst)
}

func extractTar(stream io.Reader, dir string, removeFirst bool) error {
	tarReader := tar.NewReader(stream)
	prefix := ""
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

		info := header.FileInfo()
		if removeFirst && info.IsDir() && prefix == "" {
			prefix = header.Name
			continue
		}

		path, err := securejoin.SecureJoin(dir, strings.TrimPrefix(header.Name, prefix))
		if err != nil {
			return err
		}

		if info.IsDir() {
			if removeFirst && prefix == "" {
				prefix = path
				continue
			}
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				return err
			}
			continue
		}

		// If there is no root dir then don't consider other dirs
		removeFirst = false

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
