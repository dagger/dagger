package cloud

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatCoverage(t *testing.T) {
	listing := ModuleListing{
		Name:        "maven",
		Ref:         "dagger.io/java/maven",
		Description: "Run tests and builds with Maven",
		Coverage: []CapabilityCoverage{
			{
				Capability: "verify", Status: CoverageCovered, Score: 0.9,
				Role: ArtifactRoleCheck, URI: "dag+check://?check=maven/projects/verify", Type: "Check",
				Dimensions:  []ModuleDimension{{Name: "maven-project", KeyName: "path", KeyDescription: "Path of the project."}},
				Description: "Run mvn verify. Covered lines never print this.",
			},
			{
				Capability: "publish", Status: CoveragePartial, Score: 0.4,
				Role: ArtifactRoleFunction, Object: "MavenProject", Function: "deploy",
				Description: "Deploy the project to the configured repository. Needs credentials.",
			},
			{
				Capability: "use java 21", Status: CoveragePartial, Score: 0.3,
				Role: ArtifactRoleSetting, Setting: "javaVersion",
				Description: "Java version to build with.\nDefaults to 17.",
			},
			{Capability: "sign an apk", Status: CoverageMissing, Score: 0.01},
		},
	}
	require.Equal(t, strings.Join([]string{
		`  covered  "verify"       check     dag+check://?check=maven/projects/verify [maven-project=<path>]`,
		`  partial  "publish"      function  MavenProject.deploy — Deploy the project to the configured repository.`,
		`  partial  "use java 21"  setting   [modules.maven] settings.javaVersion — Java version to build with.`,
		`  missing  "sign an apk"`,
	}, "\n"), strings.Join(FormatCoverage(listing), "\n"))

	require.Empty(t, FormatCoverage(ModuleListing{Name: "none"}), "no capabilities requested, no lines")
}

func TestFirstSentence(t *testing.T) {
	for in, want := range map[string]string{
		"":                              "",
		"One sentence":                  "One sentence",
		"First. Second.":                "First.",
		"Version 1.2 of it. Then more.": "Version 1.2 of it.",
		"Line one\nline two":            "Line one",
		"  Padded!  Rest":               "Padded!",
		"Ask? Answer.":                  "Ask?",
		"Ends with a period.":           "Ends with a period.",
		"Lint with a linter, e.g. ruff. Then more.": "Lint with a linter, e.g. ruff.",
		"The configured one (i.e. yours). Rest.":    "The configured one (i.e. yours).",
		"Go, Rust, etc. Done.":                      "Go, Rust, etc. Done.",
	} {
		require.Equal(t, want, firstSentence(in), "%q", in)
	}
}

func TestDiscoverFooter(t *testing.T) {
	require.NotContains(t, DiscoverFooter, "dagger install", "the footer names no commands")
	require.Contains(t, DiscoverFooter, "by its ref")
	require.Contains(t, DiscoverFooter, "install name")
	require.Contains(t, DiscoverFooter, "[dim=<key>]")
	require.Contains(t, DiscoverFooter, "dagger.toml")
}
