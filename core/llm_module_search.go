package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

// searchModulesToolName is the builtin that searches Dagger's published
// module index, through the same Cloud client as `dagger module search`.
const searchModulesToolName = "SearchModules"

const searchModulesDescription = "Search Dagger's published module index for a module that already does what you need, e.g. lint Python or deploy with Terraform. " +
	"Every search is logged by Dagger Cloud, query, purpose and functions included, so Dagger can see which modules people need; " +
	"it is attributed to the user's Dagger Cloud account when they are logged in, and anonymous otherwise.\n" +
	"Returns the matches, best first, as JSON: name, ref, description, docsUrl, official, and matchedOn (why it matched). " +
	"This tool installs nothing: installing a result is a separate step, using its ref."

// searchModulesNoMatch is the whole result of a search with no match. It tells
// the model the gap was recorded, so it does not spend turns rephrasing.
const searchModulesNoMatch = "No published module matches. This search was logged so Dagger can see what's missing. Don't retry with synonyms."

func searchModulesTool() LLMTool {
	return LLMTool{
		Name:        searchModulesToolName,
		Description: searchModulesDescription,
		ReadOnly:    true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Free text matched against module names, aliases and descriptions, e.g. \"terraform\". Optional: without it, modules are matched on purpose.",
				},
				"purpose": map[string]any{
					"type":        "string",
					"description": "What you are trying to do, in a sentence, e.g. \"plan infrastructure changes in CI\".",
				},
				"functions": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Functions you hope to call on the module, e.g. [\"plan\", \"apply\"]. Optional.",
				},
			},
			"required":             []string{"purpose"},
			"additionalProperties": false,
		},
	}
}

func (m *MCP) searchModulesTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Query     string   `default:""`
		Purpose   string   `default:""`
		Functions []string `default:"[]"`
	}) (any, error) {
		if strings.TrimSpace(args.Purpose) == "" {
			return nil, errors.New("purpose is required: say in a sentence what you are trying to do")
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		meta, err := query.MainClientCallerMetadata(ctx)
		if err != nil {
			return nil, err
		}
		return searchModules(ctx, meta, cloud.DiscoverModulesRequest{
			Query:     args.Query,
			Purpose:   args.Purpose,
			Functions: args.Functions,
		})
	})
}

// searchModulesResult is the compact view of a listing the model gets.
type searchModulesResult struct {
	Name        string   `json:"name"`
	Ref         string   `json:"ref"`
	Description string   `json:"description"`
	DocsURL     string   `json:"docsUrl,omitempty"`
	Official    bool     `json:"official"`
	MatchedOn   []string `json:"matchedOn"`
}

// searchModulesAuth is the credential a search sends: the client's, minus its
// OAuth refresh token. Refreshing here would spend the refresh token in the
// user's credentials file without writing the new one back, since refresh
// tokens rotate; only the session's own refresh handoff may do that. A login
// whose access token already expired searches anonymously: search is public.
func searchModulesAuth(cloudAuth *auth.Cloud) *auth.Cloud {
	if cloudAuth == nil || cloudAuth.Token == nil {
		return nil
	}
	token := *cloudAuth.Token
	switch token.TokenType {
	case "Basic", "OIDC":
		// Static credentials, never refreshed.
	default:
		if !token.Valid() {
			return nil
		}
		token.RefreshToken = ""
	}
	return &auth.Cloud{Token: &token, Org: cloudAuth.Org}
}

// searchModules sends the search with the connecting client's Cloud
// credentials, if any (never the engine's own, a tool argument, or the
// conversation's), to the Cloud URL that client is configured with.
func searchModules(ctx context.Context, meta *engine.ClientMetadata, req cloud.DiscoverModulesRequest) (string, error) {
	client, err := cloud.NewClient(ctx, searchModulesAuth(meta.CloudAuth))
	if err != nil {
		return "", errors.New("cannot initialize the Dagger Cloud client; check the session's Cloud authentication")
	}
	if meta.CloudURL != "" {
		if client, err = client.WithBaseURL(meta.CloudURL); err != nil {
			return "", err
		}
	}
	version := meta.ClientVersion
	if version == "" {
		version = engine.Version
	}
	discovery, err := client.DiscoverModules(ctx, req, cloud.DiscoverOptions{
		Kind:       cloud.DiscoverClientAgent,
		Version:    version,
		DoNotTrack: meta.DoNotTrack || analytics.DoNotTrack(),
	})
	if err != nil {
		// Cloud response bodies, URLs and transport details stay out of what
		// the model sees, as with LoadTrace.
		var derr *cloud.DiscoverError
		if errors.As(err, &derr) {
			slog.DebugContext(ctx, "module search failed", "error", err)
			return "", errors.New(derr.Summary())
		}
		return "", err
	}
	if len(discovery.Results) == 0 {
		return searchModulesNoMatch, nil
	}
	results := make([]searchModulesResult, 0, len(discovery.Results))
	for _, listing := range discovery.Results {
		matchedOn := listing.MatchedOn
		if matchedOn == nil {
			matchedOn = []string{}
		}
		results = append(results, searchModulesResult{
			Name:        listing.Name,
			Ref:         listing.Ref,
			Description: listing.Description,
			DocsURL:     listing.DocsURL,
			Official:    listing.Official,
			MatchedOn:   matchedOn,
		})
	}
	out, err := json.Marshal(results)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
