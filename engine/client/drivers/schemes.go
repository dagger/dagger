package drivers

import (
	"fmt"
	"sort"
	"strings"
)

// Scheme describes one engine selector value for user-facing help. The catalog
// below is the single source of truth for the `--engine` flag usage, the
// `dagger help engine` topic, and shell completion. A test asserts that it
// covers the driver registry exactly, so the three surfaces cannot drift from
// the drivers that the CLI can actually use.
type Scheme struct {
	// Scheme is the key that the driver registry uses.
	Scheme string
	// Value is the form to write after `--engine=`.
	Value string
	// Note explains the value when the syntax alone is not enough.
	Note string
	// Group titles a set of related values in the help topic.
	Group string
	// Primary marks a value that the short flag usage names.
	Primary bool
	// Deprecated marks a value that remains for compatibility. Help and
	// completion leave these out; the help topic still lists them.
	Deprecated bool
}

const (
	groupCloud     = "Dagger Cloud"
	groupImage     = "Start from an OCI image"
	groupContainer = "Use a running engine container"
	groupDirect    = "Connect directly"
	groupLegacy    = "Legacy Docker schemes"
)

var schemeCatalog = []Scheme{
	{Scheme: "dagger-cloud", Value: "cloud", Group: groupCloud, Primary: true},

	{Scheme: "image", Value: "image://IMAGE", Group: groupImage, Primary: true},
	{Scheme: "image+docker", Value: "image+docker://IMAGE", Group: groupImage},
	{Scheme: "image+apple", Value: "image+apple://IMAGE", Group: groupImage},
	{Scheme: "image+podman", Value: "image+podman://IMAGE", Group: groupImage},
	{Scheme: "image+finch", Value: "image+finch://IMAGE", Group: groupImage},
	{Scheme: "image+nerdctl", Value: "image+nerdctl://IMAGE", Group: groupImage},

	{Scheme: "container", Value: "container://NAME", Group: groupContainer, Primary: true},
	{Scheme: "container+docker", Value: "container+docker://NAME", Group: groupContainer},
	{Scheme: "container+apple", Value: "container+apple://NAME", Group: groupContainer},
	{Scheme: "container+podman", Value: "container+podman://NAME", Group: groupContainer},
	{Scheme: "container+finch", Value: "container+finch://NAME", Group: groupContainer},
	{Scheme: "container+nerdctl", Value: "container+nerdctl://NAME", Group: groupContainer},

	{Scheme: "tcp", Value: "tcp://HOST:PORT", Note: "no authentication", Group: groupDirect, Primary: true},
	{Scheme: "tls", Value: "tls://HOST[:PORT]", Group: groupDirect, Primary: true},
	{Scheme: "ssh", Value: "ssh://[USER@]HOST[:PORT]", Group: groupDirect, Primary: true},
	{Scheme: "kube-pod", Value: "kube-pod://POD", Group: groupDirect, Primary: true},
	{Scheme: "unix", Value: "unix://PATH", Group: groupDirect, Primary: true},

	{Scheme: "docker-image", Value: "docker-image://IMAGE", Group: groupLegacy, Deprecated: true},
	{Scheme: "docker-container", Value: "docker-container://NAME", Group: groupLegacy, Deprecated: true},
}

// SchemeCatalog returns the engine selector values, in help order.
func SchemeCatalog() []Scheme {
	return append([]Scheme(nil), schemeCatalog...)
}

// RegisteredSchemes returns the driver registry keys, sorted. Error messages
// use it, so it reports what the client can dial, not what help documents.
func RegisteredSchemes() []string {
	names := make([]string, 0, len(drivers))
	for name := range drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SchemePrefix returns the part of a value that a user types before the target:
// "image://" for "image://IMAGE", and "cloud" for a value that is not a URI.
func SchemePrefix(value string) string {
	if prefix, _, ok := strings.Cut(value, "://"); ok {
		return prefix + "://"
	}
	return value
}

// SchemeSummary names the main engine selector values on one line. The
// `--engine` flag usage uses it.
func SchemeSummary() string {
	var values []string
	for _, scheme := range schemeCatalog {
		if scheme.Primary {
			values = append(values, SchemePrefix(scheme.Value))
		}
	}
	return strings.Join(values, ", ")
}

// SchemeCompletions returns the values to offer for shell completion of an
// engine selector. Deprecated values are left out.
func SchemeCompletions() []string {
	var values []string
	for _, scheme := range schemeCatalog {
		if scheme.Deprecated {
			continue
		}
		values = append(values, SchemePrefix(scheme.Value))
	}
	return values
}

// SchemeHelp renders the full catalog, grouped, for a help topic.
func SchemeHelp() string {
	var sb strings.Builder
	group := ""
	for _, scheme := range schemeCatalog {
		if scheme.Group != group {
			if group != "" {
				sb.WriteString("\n")
			}
			group = scheme.Group
			fmt.Fprintf(&sb, "  %s:\n", group)
		}
		fmt.Fprintf(&sb, "    %s", scheme.Value)
		if scheme.Note != "" {
			fmt.Fprintf(&sb, " (%s)", scheme.Note)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
