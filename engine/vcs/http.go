// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vcs

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
)

// httpClient is the default HTTP client, but a variable so it can be
// changed by tests, without modifying http.DefaultClient.
var httpClient = http.DefaultClient

// httpsOrHTTP returns the body of either the importPath's
// https resource or, if unavailable, the http resource.
func httpsOrHTTP(ctx context.Context, importPath string) (urlStr string, body io.ReadCloser, err error) {
	fetch := func(scheme string) (urlStr string, res *http.Response, err error) {
		u, err := url.Parse(scheme + "://" + importPath)
		if err != nil {
			return "", nil, err
		}
		u.RawQuery = "go-get=1"
		urlStr = u.String()
		if Verbose {
			log.Printf("Fetching %s", urlStr)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return "", nil, err
		}
		res, err = httpClient.Do(req)
		return
	}
	closeBody := func(res *http.Response) {
		if res != nil {
			res.Body.Close()
		}
	}
	urlStr, res, err := fetch("https")
	if err != nil || res.StatusCode != 200 {
		if Verbose {
			if err != nil {
				log.Printf("https fetch failed.")
			} else {
				log.Printf("ignoring https fetch with status code %d", res.StatusCode)
			}
		}
		closeBody(res)
		urlStr, res, err = fetch("http")
	}
	if err != nil {
		closeBody(res)
		return "", nil, err
	}
	// Note: accepting a non-200 OK here, so people can serve a
	// meta import in their http 404 page.
	if Verbose {
		log.Printf("Parsing meta tags from %s (status code %d)", urlStr, res.StatusCode)
	}
	return urlStr, res.Body, nil
}
