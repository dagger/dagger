// Package main fetches a URL and streams the body to stdout — a stand-in for
// webFetch/curl inside the QA sandbox (scratch tool, lives under gitignored
// tmp/).
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fetch <url>")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		fmt.Fprintln(os.Stderr, "copy:", err)
		os.Exit(1)
	}
}
