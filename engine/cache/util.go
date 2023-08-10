package cache

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var (
	urlRegex = regexp.MustCompile("(https://[^/]*)/[^ ]*")
)

func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("unexpected status code: %d: %s",
		resp.StatusCode,
		// strip away URL paths to avoid leaking pre-signed URLs
		urlRegex.ReplaceAllString(string(body), "$1/*****"),
	)
}
