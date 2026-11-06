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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, newDiscoverError(c.u, err)
	}
	if data.DiscoverModules == nil {
		return nil, &DiscoverError{URL: c.u.Redacted(), Empty: true, Err: errors.New("no discoverModules in the response")}
	}
	if data.DiscoverModules.Results == nil {
		data.DiscoverModules.Results = []ModuleListing{}
	}
	return data.DiscoverModules, nil
}

// DiscoverError is a module search Dagger Cloud failed. Error is for a user
// at a terminal. Summary leaves out response bodies, URLs and transport or
// credential-refresh details, for readers that should not see them: the agent
// tool hands it to the model.
type DiscoverError struct {
	URL string
	// Unauthorized means Cloud rejected the credentials sent.
	Unauthorized bool
	// Status is the HTTP status Cloud answered with, when not 2xx.
	Status string
	// Messages are the GraphQL errors Cloud returned.
	Messages []string
	// Empty means Cloud answered without a result.
	Empty bool
	Err   error
}

func newDiscoverError(u *url.URL, err error) *DiscoverError {
	derr := &DiscoverError{URL: u.Redacted(), Err: err}
	var httpErr *GraphQLHTTPError
	var gqlErrs *GraphQLErrors
	switch {
	case errors.As(err, &httpErr):
		derr.Status = httpErr.Status
		derr.Unauthorized = httpErr.StatusCode == http.StatusUnauthorized
	case errors.As(err, &gqlErrs):
		derr.Messages = gqlErrs.Messages
	}
	return derr
}

const (
	discoverErrorPrefix = "search the Dagger module index"
	discoverErrorSuffix = "; module search needs Dagger Cloud and has no offline fallback"
)

func (e *DiscoverError) unauthorized() string {
	return fmt.Sprintf("%s: Dagger Cloud rejected the Cloud credentials (%s); run 'dagger login' again or fix DAGGER_CLOUD_TOKEN", discoverErrorPrefix, e.Status)
}

func (e *DiscoverError) Error() string {
	if e.Unauthorized {
		return e.unauthorized()
	}
	detail := e.Err.Error()
	var httpErr *GraphQLHTTPError
	if errors.As(e.Err, &httpErr) {
		body := httpErr.Body
		if len(body) > discoverErrorBodyLimit {
			body = body[:discoverErrorBodyLimit] + "…"
		}
		detail = httpErr.Status
		if body != "" {
			detail += ": " + body
		}
	}
	return fmt.Sprintf("%s at %s: %s%s", discoverErrorPrefix, e.URL, detail, discoverErrorSuffix)
}

// Summary is Error without what Cloud's response body, the URL or the
// transport said.
func (e *DiscoverError) Summary() string {
	var detail string
	switch {
	case e.Unauthorized:
		return e.unauthorized()
	case e.Status != "":
		detail = "Dagger Cloud answered " + e.Status
	case len(e.Messages) > 0:
		detail = "Dagger Cloud returned an error: " + strings.Join(e.Messages, "; ")
	case e.Empty:
		detail = "Dagger Cloud returned no result"
	default:
		detail = "could not reach Dagger Cloud"
	}
	return discoverErrorPrefix + ": " + detail + discoverErrorSuffix
}

func (e *DiscoverError) Unwrap() error { return e.Err }

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
