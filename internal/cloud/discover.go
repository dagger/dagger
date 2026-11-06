package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/shurcooL/graphql"
	"go.opentelemetry.io/otel/propagation"
)

// DiscoverClientKind names what is searching the module index. It goes in
// the User-Agent, so Cloud can tell CLI searches from agent searches.
type DiscoverClientKind string

const (
	DiscoverClientCLI   DiscoverClientKind = "cli"
	DiscoverClientAgent DiscoverClientKind = "agent"
)

// ModuleKind is a listing's ModuleKind enum value.
type ModuleKind string

const (
	ModuleKindModule ModuleKind = "MODULE"
	ModuleKindSDK    ModuleKind = "SDK"
)

// ModuleListing is an entry in the public module index.
type ModuleListing struct {
	Name        string     `json:"name"`
	Kind        ModuleKind `json:"kind"`
	Ref         string     `json:"ref"`
	Aliases     []string   `json:"aliases"`
	Description string     `json:"description"`
	DocsURL     string     `json:"docsUrl,omitempty"`
	Publisher   string     `json:"publisher"`
	Official    bool       `json:"official"`
	MatchedOn   []string   `json:"matchedOn"`
}

// ModuleDiscovery is the result of one logged search of the module index.
type ModuleDiscovery struct {
	// ID of the logged search event.
	ID string `json:"id"`
	// Best match first. Empty means no listing matched.
	Results []ModuleListing `json:"results"`
}

// DiscoverModulesRequest is what a caller searches for. Cloud logs all of it.
type DiscoverModulesRequest struct {
	// Free text matched against name, aliases and description.
	Query string
	// What the caller is trying to do.
	Purpose string
	// Functions the caller hopes to call.
	Functions []string
}

// DiscoverOptions describe the client making a search, sent as headers rather
// than arguments.
type DiscoverOptions struct {
	Kind    DiscoverClientKind
	Version string
	// DoNotTrack is the telemetry opt-out. It sends DNT: 1 and no traceparent,
	// so Cloud stores the search without user, org or trace, even when the
	// request carries credentials.
	DoNotTrack bool
}

// DiscoverUserAgent is the User-Agent of a module search. Cloud reads its
// first product token as <kind>/<version>: "dagger/v0.19.0" for the CLI,
// "dagger-agent/v0.19.0" for the agent tool.
func DiscoverUserAgent(kind DiscoverClientKind, version string) string {
	if version == "" {
		version = "unknown"
	}
	product := "dagger"
	if kind != "" && kind != DiscoverClientCLI {
		product += "-" + string(kind)
	}
	return product + "/" + version
}

const discoverModulesQuery = `query DiscoverModules($query: String, $purpose: String, $functions: [String!]) {
  discoverModules(query: $query, purpose: $purpose, functions: $functions) {
    id
    results {
      name
      kind
      ref
      aliases
      description
      docsUrl
      publisher
      official
      matchedOn
    }
  }
}`

// discoverErrorBodyLimit bounds how much of an HTTP error body reaches the
// user (or the model): a proxy or load balancer may answer with a page of HTML.
const discoverErrorBodyLimit = 200

// DiscoverModules searches Dagger Cloud's public module index. Cloud logs
// every search. It is the one client both `dagger module search` and the
// agent's module search tool use. There is no offline fallback: when Cloud
// fails, so does the search.
func (c *Client) DiscoverModules(ctx context.Context, req DiscoverModulesRequest, opts DiscoverOptions) (*ModuleDiscovery, error) {
	variables := map[string]any{}
	if query := strings.TrimSpace(req.Query); query != "" {
		variables["query"] = query
	}
	if purpose := strings.TrimSpace(req.Purpose); purpose != "" {
		variables["purpose"] = purpose
	}
	var functions []string
	for _, fn := range req.Functions {
		if fn = strings.TrimSpace(fn); fn != "" {
			functions = append(functions, fn)
		}
	}
	if len(functions) > 0 {
		variables["functions"] = functions
	}

	header := http.Header{}
	header.Set("User-Agent", DiscoverUserAgent(opts.Kind, opts.Version))
	if opts.DoNotTrack {
		header.Set("DNT", "1")
	} else {
		propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(header))
	}

	var data struct {
		DiscoverModules *ModuleDiscovery `json:"discoverModules"`
	}
	if err := c.doGraphQLWithHeader(ctx, "DiscoverModules", discoverModulesQuery, variables, header, &data); err != nil {
		return nil, discoverError(ctx, c.u, err)
	}
	if data.DiscoverModules == nil {
		return nil, fmt.Errorf("search the Dagger module index: Dagger Cloud returned no result; module search needs Dagger Cloud and has no offline fallback")
	}
	if data.DiscoverModules.Results == nil {
		data.DiscoverModules.Results = []ModuleListing{}
	}
	return data.DiscoverModules, nil
}

func discoverError(ctx context.Context, u *url.URL, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var httpErr *GraphQLHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("search the Dagger module index: Dagger Cloud rejected your credentials (%s); run 'dagger login' again or fix DAGGER_CLOUD_TOKEN", httpErr.Status)
		}
		body := httpErr.Body
		if len(body) > discoverErrorBodyLimit {
			body = body[:discoverErrorBodyLimit] + "…"
		}
		err = fmt.Errorf("%s: %s", httpErr.Status, body)
	}
	return fmt.Errorf("search the Dagger module index at %s: %w; module search needs Dagger Cloud and has no offline fallback", u.Redacted(), err)
}

// WithBaseURL returns a copy of the client that talks to the Cloud API at
// base instead of the one DAGGER_CLOUD_URL resolved to: the engine uses it to
// reach the Cloud URL its client is configured with.
func (c *Client) WithBaseURL(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse cloud URL %q: %w", base, err)
	}
	clone := *c
	clone.u = u
	clone.g = graphql.NewClient(u.JoinPath("/query").String(), c.h)
	return &clone, nil
}
