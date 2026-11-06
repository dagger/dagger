package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud"
)

var (
	searchSDKOnly      bool
	searchPurpose      string
	searchCapabilities []string
	searchJSON         bool
)

// moduleSearchNoMatch is printed when the index has no match. Every search is
// a demand signal, so say that it was recorded.
const moduleSearchNoMatch = "No published module matches. This search was logged so Dagger can see what's missing."

// moduleSearchNoSDKMatch is printed when --sdk filters out every match.
const moduleSearchNoSDKMatch = "No published SDK matches, though other modules do."

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search for modules you can install",
	Long: `Search the Dagger module index on Dagger Cloud by name, alias, description
or artifacts.

Say what you are trying to do with --for, and what you need the module to do
with --capability: each result shows whether it covers each capability, and
with which check, generator, function or setting. It also tells Dagger which
modules people need.

Searches are logged to help Dagger see which modules people need: the query,
--for and --capability values are recorded by Dagger Cloud. When you are logged
in to Dagger Cloud, the search is attributed to your account; otherwise it is
anonymous. With DO_NOT_TRACK=1, no account, org or trace is recorded.

Search needs Dagger Cloud: there is no offline fallback.

With no query and no --for, lists all published modules.`,
	Example: `dagger module search wolfi
dagger module search --for "lint Python code in CI" --capability lint --capability format`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := moduleSearchOpts{
			Purpose:      searchPurpose,
			Capabilities: searchCapabilities,
			SDKOnly:      searchSDKOnly,
			JSON:         searchJSON,
		}
		if len(args) == 1 {
			opts.Query = args[0]
		}
		client, err := moduleSearchClient(cmd.Context())
		if err != nil {
			return err
		}
		return moduleSearch(cmd.Context(), client, cmd.OutOrStdout(), opts)
	},
}

func init() {
	searchCmd.Flags().StringVar(&searchPurpose, "for", "", "What you are trying to do, e.g. \"lint Python code in CI\"")
	searchCmd.Flags().StringArrayVar(&searchCapabilities, "capability", nil, "Something you need the module to do, e.g. \"lint\" (repeatable)")
	searchCmd.Flags().BoolVar(&searchSDKOnly, "sdk", false, "Only show modules that provide SDK capabilities")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Print results as JSON")
}

type moduleSearchOpts struct {
	Query        string
	Purpose      string
	Capabilities []string
	SDKOnly      bool
	JSON         bool
}

// moduleSearchClient is a Cloud client carrying the user's Cloud credentials
// when there are any. Search is public, so a missing or unreadable credential
// searches anonymously rather than failing or prompting for a login.
func moduleSearchClient(ctx context.Context) (*cloud.Client, error) {
	cloudAuth, err := cloudCLI.cloudAuthWithLogin(ctx, false)
	if err != nil {
		if !errors.Is(err, errCloudNotAuthenticated) {
			slog.Warn("could not read Dagger Cloud credentials; searching anonymously", "error", err)
		}
		cloudAuth = nil
	}
	client, err := cloud.NewClient(ctx, cloudAuth)
	if err != nil {
		return nil, fmt.Errorf("cloud client: %w", err)
	}
	return client, nil
}

func moduleSearch(ctx context.Context, client *cloud.Client, out io.Writer, opts moduleSearchOpts) error {
	discovery, err := client.DiscoverModules(ctx, cloud.DiscoverModulesRequest{
		Query:        opts.Query,
		Purpose:      opts.Purpose,
		Capabilities: opts.Capabilities,
	}, cloud.DiscoverOptions{
		Kind:       cloud.DiscoverClientCLI,
		Version:    engine.Version,
		DoNotTrack: analytics.DoNotTrack(),
	})
	if err != nil {
		return err
	}
	if opts.SDKOnly {
		sdks := []cloud.ModuleListing{}
		for _, listing := range discovery.Results {
			if listing.Kind == cloud.ModuleKindSDK {
				sdks = append(sdks, listing)
			}
		}
		if len(sdks) == 0 && len(discovery.Results) > 0 && !opts.JSON {
			// Modules matched, just no SDK: don't claim nothing did.
			_, err := fmt.Fprintln(out, moduleSearchNoSDKMatch)
			return err
		}
		discovery.Results = sdks
	}
	if opts.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(discovery)
	}
	return printModuleSearchResults(out, discovery.Results)
}

func printModuleSearchResults(out io.Writer, listings []cloud.ModuleListing) error {
	if len(listings) == 0 {
		_, err := fmt.Fprintln(out, moduleSearchNoMatch)
		return err
	}

	// A table of sources, each followed by its coverage of the requested
	// capabilities, if any. Sources are padded by hand: the coverage lines
	// between them would split a tabwriter's columns.
	const sourceHeader = "SOURCE"
	width := len(sourceHeader)
	for _, listing := range listings {
		width = max(width, len(listing.Ref))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %s\n", width, sourceHeader, "DESCRIPTION")
	var coverage bool
	for _, listing := range listings {
		fmt.Fprintf(&b, "%-*s  %s\n", width, listing.Ref, listing.Description)
		for _, line := range cloud.FormatCoverage(listing) {
			b.WriteString(line + "\n")
			coverage = true
		}
	}
	if coverage {
		// The legend for links, dimension hints and settings, as the agent
		// tool gets it.
		b.WriteString("\n" + cloud.DiscoverFooter + "\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}
