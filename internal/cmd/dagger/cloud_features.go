package daggercmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cloudapi "github.com/dagger/dagger/internal/cloud"
)

// cloudFeature is a Dagger Cloud capability an org may have enabled (mirrors
// the API's FeatureName enum).
type cloudFeature string

const (
	featureCloudChecks  cloudFeature = "CLOUD_CHECKS"
	featureCloudModules cloudFeature = "CLOUD_MODULES"
)

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

// ensureCommandCloudFeatures verifies orgName has every feature cmd requires,
// and fails with an actionable error when some are missing. Nothing is
// enabled here: Cloud enables the features an organization created from the
// CLI needs (see createNewOrg). A command with no declared requirements is a
// no-op.
func ensureCommandCloudFeatures(cmd *cobra.Command, client *cloudapi.Client, orgName string) error {
	features := commandCloudFeatures(cmd)
	if len(features) == 0 {
		return nil
	}
	details, err := client.OrgDetails(cmd.Context(), orgName)
	if err != nil {
		return fmt.Errorf("lookup Cloud org %q: %w", orgName, err)
	}
	missing := make([]cloudFeature, 0, len(features))
	for _, feature := range features {
		if !orgFeatureEnabled(details, feature) {
			missing = append(missing, feature)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missingFeaturesError(details, missing)
}

// featureNames converts features to the plain strings the API uses.
func featureNames(features []cloudFeature) []string {
	names := make([]string, 0, len(features))
	for _, feature := range features {
		names = append(names, string(feature))
	}
	return names
}

// joinFeatures renders a feature list for humans, e.g.
// "CLOUD_CHECKS + CLOUD_MODULES".
func joinFeatures(features []cloudFeature) string {
	return strings.Join(featureNames(features), " + ")
}

// missingFeaturesError explains why required features are missing and what to
// do: enter payment details after a trial ended, otherwise see the org's
// settings.
func missingFeaturesError(details *cloudapi.OrgDetails, missing []cloudFeature) error {
	verb := "is"
	if len(missing) > 1 {
		verb = "are"
	}
	for _, feature := range missing {
		if orgFeatureStatus(details, feature) == "TRIAL_EXPIRED" {
			return fmt.Errorf("the free trial of organization %q has ended, so %s %s no longer enabled; enter payment details to continue:\n\n  dagger cloud billing payment",
				details.Name, joinFeatures(missing), verb)
		}
	}
	return fmt.Errorf("%s %s not enabled for organization %q; see https://dagger.cloud/%s/settings",
		joinFeatures(missing), verb, details.Name, details.Name)
}

// orgFeatureStatus returns the org's status for feature, or "" when it has none.
func orgFeatureStatus(details *cloudapi.OrgDetails, feature cloudFeature) string {
	for _, f := range details.Features {
		if f.Name == string(feature) {
			return f.Status
		}
	}
	return ""
}

// orgFeatureEnabled reports whether the org has the feature usable right now:
// ACTIVE or IN_TRIAL, mirroring the API's Org.HasFeatureEnabled.
func orgFeatureEnabled(details *cloudapi.OrgDetails, feature cloudFeature) bool {
	status := orgFeatureStatus(details, feature)
	return status == "ACTIVE" || status == "IN_TRIAL"
}
