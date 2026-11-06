package core

import (
	"context"
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
	"Every search is logged by Dagger Cloud, query, purpose and capabilities included, so Dagger can see which modules people need; " +
	"it is attributed to the user's Dagger Cloud account when they are logged in, and anonymous otherwise.\n" +
	"Returns the matches, best first, one line each: name, ref, description. " +
	"Under each, one line per requested capability: covered, partial or missing, then the role and the DAG link, Object.function or dagger.toml setting that provides it. " +
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
					"description": "Free text matched against module names, aliases, descriptions and artifacts, e.g. \"terraform\". Optional: without it, modules are matched on purpose.",
				},
				"purpose": map[string]any{
					"type":        "string",
					"description": "What you are trying to do, in a sentence, e.g. \"plan infrastructure changes in CI\".",
				},
				"capabilities": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Short capabilities you need from the module, in your own words, e.g. [\"lint\", \"apply a saved plan\"]. Each result reports how it covers each one. Optional.",
				},
			},
			"required":             []string{"purpose"},
			"additionalProperties": false,
		},
	}
}

func (m *MCP) searchModulesTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Query        string   `default:""`
		Purpose      string   `default:""`
		Capabilities []string `default:"[]"`
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
			Query:        args.Query,
			Purpose:      args.Purpose,
			Capabilities: args.Capabilities,
		})
	})
}

// formatSearchModules is the compact text the model gets: a line per module,
// its coverage lines beneath it, and the footer once.
func formatSearchModules(results []cloud.ModuleListing) string {
	if len(results) == 0 {
		return searchModulesNoMatch
	}
	var b strings.Builder
	for _, listing := range results {
		b.WriteString(listing.Name + "  " + listing.Ref + "  " + listing.Description + "\n")
		for _, line := range cloud.FormatCoverage(listing) {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n" + cloud.DiscoverFooter)
	return b.String()
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
	return formatSearchModules(discovery.Results), nil
}
