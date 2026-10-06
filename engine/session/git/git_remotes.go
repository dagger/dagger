package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// Read routing with the checkout's own Git, including its URL rewrites. This
// neither contacts a remote nor exposes unrestricted host configuration.
func checkoutRemoteMetadata(ctx context.Context, checkout, headRef string) ([]*CheckoutRemote, string, string, error) {
	out, err := runHostGit(ctx, checkout, "remote")
	if err != nil {
		return nil, "", "", fmt.Errorf("read checkout remote names: %w", err)
	}
	remotes := make([]*CheckoutRemote, 0)
	fingerprint := sha256.New()
	for _, name := range strings.Fields(out) {
		fetch, err := runHostGit(ctx, checkout, "remote", "get-url", name)
		if err != nil {
			return nil, "", "", fmt.Errorf("read checkout fetch destination: %w", err)
		}
		push, err := runHostGit(ctx, checkout, "remote", "get-url", "--push", name)
		if err != nil {
			return nil, "", "", fmt.Errorf("read checkout push destination: %w", err)
		}
		// Omitted credential-bearing URLs still affect checkout identity.
		// Their text never leaves the client as remote metadata.
		_, _ = fingerprint.Write([]byte(strings.Join([]string{name, fetch, push, ""}, "\x00")))
		fetchURL := checkoutRoutingURL(strings.TrimSpace(fetch))
		pushURL := checkoutRoutingURL(strings.TrimSpace(push))
		if pushURL == fetchURL {
			pushURL = ""
		}
		remotes = append(remotes, &CheckoutRemote{Name: name, Url: fetchURL, PushUrl: pushURL})
	}
	var upstream string
	if strings.HasPrefix(headRef, "refs/heads/") {
		out, err := runHostGit(ctx, checkout, "for-each-ref", "--format=%(upstream:remotename)", "--", headRef)
		if err != nil {
			return nil, "", "", fmt.Errorf("read checkout upstream: %w", err)
		}
		upstream = strings.TrimSpace(out)
	}
	return remotes, upstream, hex.EncodeToString(fingerprint.Sum(nil)), nil
}

func checkoutRoutingURL(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if parsed.User != nil {
		_, password := parsed.User.Password()
		if password || !strings.EqualFold(parsed.Scheme, "ssh") {
			return ""
		}
	}
	return raw
}
