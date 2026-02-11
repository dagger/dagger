package main

import (
	"context"
	"fmt"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	cloud "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/spf13/cobra"
)

var traceOrgFlag string

var traceCmd = &cobra.Command{
	Use:    "trace [trace ID]",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Annotations: map[string]string{
		"experimental": "true",
	},
	Aliases: []string{"t"},
	Short:   "View a Dagger trace from Dagger Cloud.",
	GroupID: cloudGroup.ID,
	Example: `dagger trace 2f123ba77bf7bd2d4db2f70ed20613e8`,
	RunE:    Trace,
}

func init() {
	traceCmd.Flags().StringVar(&traceOrgFlag, "org", "", "Dagger Cloud org name (defaults to current org)")
}

func Trace(cmd *cobra.Command, args []string) error {
	traceID := args[0]

	return Frontend.Run(cmd.Context(), dagui.FrontendOpts{
		Verbosity: dagui.ShowCompletedVerbosity,
		NoExit:    true,
	}, func(ctx context.Context) (cleanups.CleanupF, error) {
		cloudAuth, err := auth.GetCloudAuth(ctx)
		if err != nil {
			return nil, fmt.Errorf("cloud auth: %w", err)
		}
		if cloudAuth == nil || cloudAuth.Token == nil {
			return nil, fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
		}

		client, err := cloud.NewClient(ctx, cloudAuth)
		if err != nil {
			return nil, fmt.Errorf("cloud client: %w", err)
		}

		// Resolve org ID: --org flag > current org
		orgID, err := resolveOrgID(ctx, client, cloudAuth)
		if err != nil {
			return nil, err
		}

		exp := Frontend.SpanExporter()

		noop := func() error { return nil }

		err = client.StreamSpans(ctx, orgID, traceID, func(spanDatas []cloud.SpanData) {
			slog.Debug("received spans from cloud", "count", len(spanDatas))

			// Convert to OTLP proto, then to OTel SDK ReadOnlySpans,
			// and feed through the frontend's exporter pipeline so
			// rendering is triggered correctly.
			resourceSpans := cloud.SpansToPB(spanDatas)
			spans := telemetry.SpansFromPB(resourceSpans)
			if len(spans) == 0 {
				return
			}

			if err := exp.ExportSpans(ctx, spans); err != nil {
				slog.Warn("error exporting spans", "err", err)
				return
			}

			// Find the root span (no parent) and set it as the primary span
			// so the TUI shows the trace tree rooted there.
			for _, span := range spans {
				if !span.Parent().SpanID().IsValid() {
					spanID := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
					slog.Debug("setting primary span", "spanID", spanID)
					Frontend.SetPrimary(spanID)
					break
				}
			}
		})
		if err != nil {
			return noop, fmt.Errorf("stream spans: %w", err)
		}

		return noop, nil
	})
}

func resolveOrgID(ctx context.Context, client *cloud.Client, cloudAuth *auth.Cloud) (string, error) {
	if traceOrgFlag != "" {
		// Resolve org name to ID via GraphQL
		org, err := client.OrgByName(ctx, traceOrgFlag)
		if err != nil {
			return "", fmt.Errorf("resolve org %q: %w", traceOrgFlag, err)
		}
		return org.ID, nil
	}

	// Fall back to current org from auth
	if cloudAuth.Org != nil && cloudAuth.Org.ID != "" {
		return cloudAuth.Org.ID, nil
	}

	return "", fmt.Errorf("no org specified; use --org or run 'dagger login' to set a default org")
}
