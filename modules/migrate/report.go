package main

import (
	"fmt"
	"strings"
)

// reportBuilder accumulates migration report entries and renders them as markdown.
type reportBuilder struct {
	moduleName string
	oldSource  string
	newSource  string
	depCount   int
	includeCount int
	toolchains []toolchainReportEntry
	warnings   []warning
	removedFiles []string
}

type toolchainReportEntry struct {
	name    string
	source  string
	status  string
}

func (r *reportBuilder) setProjectModule(name, oldSource, newSource string) {
	r.moduleName = name
	r.oldSource = oldSource
	r.newSource = newSource
}

func (r *reportBuilder) setRewrittenDeps(count int) {
	r.depCount = count
}

func (r *reportBuilder) setRewrittenIncludes(count int) {
	r.includeCount = count
}

func (r *reportBuilder) addToolchain(name, source string, warningCount int) {
	status := "migrated"
	if warningCount > 0 {
		status = fmt.Sprintf("migrated (%d warning)", warningCount)
		if warningCount > 1 {
			status = fmt.Sprintf("migrated (%d warnings)", warningCount)
		}
	}
	r.toolchains = append(r.toolchains, toolchainReportEntry{
		name:   name,
		source: source,
		status: status,
	})
}

func (r *reportBuilder) addRemovedFile(path string) {
	r.removedFiles = append(r.removedFiles, path)
}

func (r *reportBuilder) setWarnings(warnings []warning) {
	r.warnings = warnings
}

func (r *reportBuilder) String() string {
	var b strings.Builder

	b.WriteString("# Migration Report\n\n")
	b.WriteString("Migrated from legacy `dagger.json` to workspace format.\n\n")

	// Project module section
	if r.moduleName != "" && r.oldSource != "" {
		fmt.Fprintf(&b, "## Project Module: %s\n\n", r.moduleName)
		fmt.Fprintf(&b, "- Moved source: `%s` -> `%s`\n", r.oldSource, r.newSource)
		b.WriteString("- Updated `dagger.json`: removed `source`, `toolchains` fields\n")
		if r.depCount > 0 {
			fmt.Fprintf(&b, "- Rewrote %d dependency paths (relative to new location)\n", r.depCount)
		}
		if r.includeCount > 0 {
			fmt.Fprintf(&b, "- Rewrote %d include paths\n", r.includeCount)
		}
		b.WriteString("\n")
	}

	// Toolchains section
	if len(r.toolchains) > 0 {
		b.WriteString("## Toolchains -> Workspace Modules\n\n")
		b.WriteString("| Toolchain | Source | Status |\n")
		b.WriteString("|-----------|--------|--------|\n")
		for _, tc := range r.toolchains {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", tc.name, tc.source, tc.status)
		}
		b.WriteString("\n")
	}

	// Warnings section
	if len(r.warnings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, w := range r.warnings {
			fmt.Fprintf(&b, "- **%s**: %s\n", w.toolchain, w.message)
		}
		b.WriteString("\n")
	}

	// Removed files
	if len(r.removedFiles) > 0 {
		b.WriteString("## Removed Files\n\n")
		for _, f := range r.removedFiles {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	// Not migrated section
	b.WriteString("## Not Migrated (manual action needed)\n\n")
	b.WriteString("- [ ] Aliases: project module has `alias = true` set; verify all functions are promoted correctly\n")
	b.WriteString("- [ ] User defaults (.env): review and migrate manually to `config.*` entries\n")

	return b.String()
}
