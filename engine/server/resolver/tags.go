package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/distribution/reference"
)

const (
	registryTagsPageSize    = 1000
	maxRegistryTagListPages = 10000
	maxRegistryTagListBody  = 16 << 20
	maxRegistryTagCount     = 1_000_000
)

type ListImageTagsOpts struct {
	Network           NetworkConfig
	RegistryTransport RegistryTransport
}

// ListImageTags lists every tag for an image repository using the same
// registry hosts, authentication, transport, and network as image pulls.
func (r *Resolver) ListImageTags(ctx context.Context, ref string, opts ListImageTagsOpts) ([]string, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return nil, fmt.Errorf("parse image repository %q: %w", ref, err)
	}
	repository := reference.TrimNamed(named)
	domain := reference.Domain(repository)
	repositoryPath := reference.Path(repository)

	hosts, err := r.registryHosts(opts.Network, opts.RegistryTransport)(domain)
	if err != nil {
		return nil, fmt.Errorf("resolve registry hosts for %q: %w", ref, err)
	}

	var hostErrors []string
	for _, host := range hosts {
		if !host.Capabilities.Has(docker.HostCapabilityResolve) {
			continue
		}
		tags, err := listImageTagsFromHost(ctx, host, domain, repositoryPath)
		if err == nil {
			return tags, nil
		}
		hostErrors = append(hostErrors, fmt.Sprintf("%s: %v", host.Host, err))
	}
	if len(hostErrors) == 0 {
		return nil, fmt.Errorf("no registry host can list tags for %q", ref)
	}
	return nil, fmt.Errorf("list tags for %q: %s", ref, strings.Join(hostErrors, "; "))
}

func listImageTagsFromHost(ctx context.Context, host docker.RegistryHost, domain, repository string) ([]string, error) {
	next := &url.URL{
		Scheme: host.Scheme,
		Host:   host.Host,
		Path:   path.Join("/", host.Path, repository, "tags/list"),
	}
	query := next.Query()
	query.Set("n", fmt.Sprint(registryTagsPageSize))
	if host.Host != domain && !(domain == "docker.io" && host.Host == "registry-1.docker.io") {
		query.Set("ns", domain)
	}
	next.RawQuery = query.Encode()

	seen := map[string]struct{}{}
	var tags []string
	for range maxRegistryTagListPages {
		if _, ok := seen[next.String()]; ok {
			return nil, fmt.Errorf("registry returned a pagination loop at %q", next)
		}
		seen[next.String()] = struct{}{}

		resp, err := doRegistryRequest(ctx, host, next)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}

		var page struct {
			Tags []string `json:"tags"`
		}
		decoder := json.NewDecoder(io.LimitReader(resp.Body, maxRegistryTagListBody))
		if err := decoder.Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode tag list from %q: %w", next, err)
		}
		resp.Body.Close()
		if len(page.Tags) > maxRegistryTagCount || len(tags) > maxRegistryTagCount-len(page.Tags) {
			resp.Body.Close()
			return nil, fmt.Errorf("registry tag listing exceeded %d tags", maxRegistryTagCount)
		}

		tags = append(tags, page.Tags...)

		next, err = registryNextPage(resp, next)
		if err != nil {
			return nil, err
		}
		if next == nil {
			return tags, nil
		}
		if next.Scheme != host.Scheme || next.Host != host.Host {
			return nil, fmt.Errorf("registry pagination escaped host to %q", next)
		}
	}
	return nil, fmt.Errorf("registry tag listing exceeded %d pages", maxRegistryTagListPages)
}

func doRegistryRequest(ctx context.Context, host docker.RegistryHost, target *url.URL) (*http.Response, error) {
	client := host.Client
	if client == nil {
		client = http.DefaultClient
	}
	var responses []*http.Response
	for range 6 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, err
		}
		for key, values := range host.Header {
			req.Header[key] = append(req.Header[key], values...)
		}
		req.Header.Set("Accept", "application/json")
		if host.Authorizer != nil {
			if err := host.Authorizer.Authorize(ctx, req); err != nil {
				return nil, fmt.Errorf("authorize tag list: %w", err)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		responses = append(responses, resp)
		if resp.StatusCode != http.StatusUnauthorized || host.Authorizer == nil {
			return resp, nil
		}
		if err := host.Authorizer.AddResponses(ctx, responses); err != nil {
			return resp, nil
		}
		resp.Body.Close()
	}
	return nil, fmt.Errorf("registry authentication exceeded retry limit")
}

func registryNextPage(resp *http.Response, base *url.URL) (*url.URL, error) {
	for _, part := range strings.Split(resp.Header.Get("Link"), ",") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.IndexByte(part, '<')
		end := strings.IndexByte(part, '>')
		if start == -1 || end <= start+1 {
			return nil, fmt.Errorf("invalid registry pagination link %q", part)
		}
		next, err := url.Parse(part[start+1 : end])
		if err != nil {
			return nil, fmt.Errorf("parse registry pagination link %q: %w", part, err)
		}
		return base.ResolveReference(next), nil
	}
	return nil, nil
}
