package daggercmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	cloudapi "github.com/dagger/dagger/internal/cloud"
)

// cloudFeature is a Dagger Cloud capability an org may have enabled (mirrors
// the API's FeatureName enum).
type cloudFeature string

const (
	featureCloudChecks cloudFeature = "CLOUD_CHECKS"
)

// cloudFeatureTrialDays is the trial length offered when a required feature is
// missing, matching the Cloud web UI's trial duration.
const cloudFeatureTrialDays = 14

// cloudFeaturesAnnotation stores a command's required Cloud features in its
// cobra annotations (comma-separated), so requirements are declared next to
// the command definition and looked up generically at enforcement time.
const cloudFeaturesAnnotation = "dagger.io/requiredCloudFeatures"

// requireCloudFeatures declares that cmd needs the given Cloud features enabled
// on the target org. Enforcement happens via ensureCommandCloudFeatures once
// the command has resolved which org it operates on (commands resolve orgs at
// different points, so this can't run as a blanket PreRun). Adding a
// requirement to another command is a single line in its init(), e.g.:
//
//	requireCloudFeatures(cloudIntegrationCmd, featureCloudChecks)
func requireCloudFeatures(cmd *cobra.Command, features ...cloudFeature) {
	if len(features) == 0 {
		return
	}
	names := make([]string, 0, len(features))
	for _, feature := range features {
		names = append(names, string(feature))
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[cloudFeaturesAnnotation] = strings.Join(names, ",")
}

// commandCloudFeatures returns the Cloud features cmd requires (empty when none
// were declared).
func commandCloudFeatures(cmd *cobra.Command) []cloudFeature {
	raw := cmd.Annotations[cloudFeaturesAnnotation]
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	features := make([]cloudFeature, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			features = append(features, cloudFeature(part))
		}
	}
	return features
}

// ensureCommandCloudFeatures verifies orgName has every feature cmd requires.
// For each missing feature the user is prompted (auto-accepted with
// --auto-apply) to start a free trial via startFeatureTrial; declining, or a
// non-interactive run, fails with an actionable error. A command with no
// declared requirements is a no-op.
func ensureCommandCloudFeatures(cmd *cobra.Command, client *cloudapi.Client, orgName string) error {
	features := commandCloudFeatures(cmd)
	if len(features) == 0 {
		return nil
	}
	ctx := cmd.Context()
	details, err := client.OrgDetails(ctx, orgName)
	if err != nil {
		return fmt.Errorf("lookup Cloud org %q: %w", orgName, err)
	}
	for _, feature := range features {
		if orgFeatureEnabled(details, feature) {
			continue
		}
		start, err := confirmFeatureTrial(cmd, details, feature)
		if err != nil {
			return err
		}
		if !start {
			return fmt.Errorf("%s is not enabled for organization %q; enable it at https://dagger.cloud/%s/settings or re-run with --auto-apply to start a trial",
				feature, details.Name, details.Name)
		}
		if err := client.StartFeatureTrial(ctx, details.ID, string(feature), cloudFeatureTrialDays); err != nil {
			return fmt.Errorf("start %s trial for organization %q: %w", feature, details.Name, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Started a %d-day %s trial for organization %q.\n",
			cloudFeatureTrialDays, feature, details.Name)
	}
	return nil
}

type featureTrialChoice string

const (
	featureTrialStart  featureTrialChoice = "start"
	featureTrialNotNow featureTrialChoice = "not-now"
)

// confirmFeatureTrial asks whether to start a trial of the missing feature,
// rendered as the same explicit-choice prompt as the `dagger setup` Cloud
// login step. --auto-apply accepts without prompting; a non-interactive run
// declines (the caller turns that into an actionable error).
func confirmFeatureTrial(cmd *cobra.Command, details *cloudapi.OrgDetails, feature cloudFeature) (bool, error) {
	if autoApply {
		return true, nil
	}
	if !stdinIsTTY {
		return false, nil
	}
	choice := featureTrialStart
	form := huh.NewForm(huh.NewGroup(
		idtui.NewExplicitChoice(&choice,
			huh.NewOption("Start trial", featureTrialStart),
			huh.NewOption("Not now", featureTrialNotNow),
		).
			Title(fmt.Sprintf("Start a free %d-day %s trial?", cloudFeatureTrialDays, feature)).
			TitleLink(fmt.Sprintf("https://dagger.cloud/%s/settings", details.Name)).
			Description(fmt.Sprintf("Organization %q does not have %s enabled.\nMore info: https://dagger.cloud/%s/settings", details.Name, feature, details.Name)),
	))
	if err := idtui.RunStandaloneForm(cmd.Context(), form); err != nil {
		return false, err
	}
	return choice == featureTrialStart, nil
}

// orgFeatureEnabled reports whether the org has the feature usable right now:
// ACTIVE or IN_TRIAL, mirroring the API's Org.HasFeatureEnabled.
func orgFeatureEnabled(details *cloudapi.OrgDetails, feature cloudFeature) bool {
	for _, f := range details.Features {
		if f.Name == string(feature) && (f.Status == "ACTIVE" || f.Status == "IN_TRIAL") {
			return true
		}
	}
	return false
}
