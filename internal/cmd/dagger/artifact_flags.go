package daggercmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/jinzhu/inflection"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const artifactPresenceFlag = "dagger.io/artifact-presence"

type artifactFlagNames struct{ Key, Presence string }

// Allocate all names together. A flag has the same meaning in every peer command.
func artifactDimensionFlagNames(cmd *cobra.Command, dimensions artifact.Dimensions) map[string]artifactFlagNames {
	reserved := map[string]bool{}
	peers := append([]*cobra.Command{cmd}, cmd.Root().Commands()...)
	for _, peer := range peers {
		if peer != cmd && !slices.Contains([]string{"list", "check", "generate", "up", "shell", "agent"}, peer.Name()) {
			continue
		}
		flags := copyCommandFlags(peer, "reserved artifact flags")
		flags.VisitAll(func(f *pflag.Flag) {
			if len(f.Annotations[artifactDimensionFlag]) == 0 {
				reserved[f.Name] = true
			}
		})
	}
	type request struct {
		id         string
		presence   bool
		candidates []string
	}
	var requests []request
	dims := slices.Clone(dimensions)
	slices.SortFunc(dims, func(a, b *artifact.Dimension) int { return strings.Compare(a.Identifier, b.Identifier) })
	// Collection keys have priority over type names.
	for _, kind := range []string{"collection-key", "collection-presence", "type"} {
		for _, d := range dims {
			if (kind == "type") != (d.Kind == "TYPE") {
				continue
			}
			candidates := []string{d.Name, inflection.Singular(d.QualifiedName)}
			presence := kind == "collection-presence"
			if presence {
				candidates = []string{inflection.Plural(d.Name), inflection.Plural(d.QualifiedName)}
			}
			if kind == "type" {
				candidates = []string{d.Name, "artifact-" + d.Name}
			}
			requests = append(requests, request{d.Identifier, presence, candidates})
		}
	}
	names := map[string]artifactFlagNames{}
	for i, req := range requests {
		chosen := ""
		for level, candidate := range req.candidates {
			if candidate == "" || reserved[candidate] {
				continue
			}
			conflict := false
			for j, other := range requests {
				if i != j && other.presence == req.presence && strings.HasPrefix(other.id, "type:") == strings.HasPrefix(req.id, "type:") && level < len(other.candidates) && other.candidates[level] == candidate {
					conflict = true
					break
				}
			}
			if !conflict {
				chosen = candidate
				break
			}
		}
		if chosen == "" {
			chosen = "dimension-" + cliName(strings.ReplaceAll(req.id, ".", "-"))
			if req.presence {
				chosen += "-all"
			}
			base := chosen
			for n := 2; reserved[chosen]; n++ {
				chosen = fmt.Sprintf("%s-%d", base, n)
			}
		}
		reserved[chosen] = true
		n := names[req.id]
		if req.presence {
			n.Presence = chosen
		} else {
			n.Key = chosen
		}
		names[req.id] = n
	}
	return names
}
