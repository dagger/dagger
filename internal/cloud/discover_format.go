package cloud

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// DiscoverFooter explains, once, how to read the coverage lines of
// FormatCoverage: what a link's first segment is, what a dimension hint
// means, and where settings go. It names no commands.
const DiscoverFooter = "Install a module by its ref. " +
	"A link's first path segment is the module's install name; substitute yours if you install it under another name. " +
	"[dim=<key>] after a link names a collection dimension: add &dim=<key> to the link to narrow it to one item, or omit it for all items. " +
	"Settings go in dagger.toml, under the table shown."

// FormatCoverage renders a listing's coverage, one line per requested
// capability, indented and aligned:
//
//	covered  "verify"     check     dag+check://?check=maven/projects/verify [maven-project=<path>]
//	partial  "publish"    function  MavenProject.deploy — Deploy the project.
//	missing  "sign an apk"
//
// PARTIAL lines carry the first sentence of the target's description.
func FormatCoverage(listing ModuleListing) []string {
	type row struct{ status, capability, role, target string }
	rows := make([]row, 0, len(listing.Coverage))
	var statusW, capabilityW, roleW int
	for _, cov := range listing.Coverage {
		r := row{
			status:     strings.ToLower(string(cov.Status)),
			capability: fmt.Sprintf("%q", cov.Capability),
		}
		if cov.Status != CoverageMissing {
			r.role = strings.ToLower(string(cov.Role))
			r.target = coverageTarget(listing, cov)
		}
		statusW = max(statusW, utf8.RuneCountInString(r.status))
		capabilityW = max(capabilityW, utf8.RuneCountInString(r.capability))
		roleW = max(roleW, utf8.RuneCountInString(r.role))
		rows = append(rows, r)
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		line := "  " + pad(r.status, statusW) + "  " + pad(r.capability, capabilityW)
		if r.role != "" || r.target != "" {
			line += "  " + pad(r.role, roleW) + "  " + r.target
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines
}

// coverageTarget is what a capability matched: the DAG link, Object.function
// or setting, with a hint per narrowing dimension, then, when PARTIAL, the
// first sentence of its description.
func coverageTarget(listing ModuleListing, cov CapabilityCoverage) string {
	var target string
	switch {
	case cov.Role == ArtifactRoleSetting || cov.Setting != "":
		target = fmt.Sprintf("[modules.%s] settings.%s", listing.Name, cov.Setting)
	case cov.Role == ArtifactRoleFunction || cov.URI == "":
		target = cov.Object + "." + cov.Function
	default:
		target = cov.URI
	}
	for _, dim := range cov.Dimensions {
		target += fmt.Sprintf(" [%s=<%s>]", dim.Name, dim.KeyName)
	}
	if cov.Status == CoveragePartial {
		if sentence := firstSentence(cov.Description); sentence != "" {
			target += " — " + sentence
		}
	}
	return target
}

// firstSentence is the text up to the first sentence end or line break.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == '.' || s[i] == '!' || s[i] == '?') && s[i+1] == ' ' {
			return s[:i+1]
		}
	}
	return strings.TrimSpace(s)
}

func pad(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}
