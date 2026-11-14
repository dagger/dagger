package core

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
)

type artifactAddressPlan struct{ Selectors []dagaddress.Pair }
type artifactAddressPlans struct{ Typed, Untyped *artifactAddressPlan }

func (a *Artifact) addressRow(typed bool) artifact.AddressRow {
	typ := "*"
	if typed {
		typ = ArtifactTypeName(a.TypeName)
	}
	return artifact.AddressRow{Type: typ, Coordinates: a.CanonicalCoordinates()}
}

func (a *Artifact) AddressIdentity() string {
	data, _ := json.Marshal(struct {
		Path []string
		Type string
		Row  artifact.AddressRow
	}{a.Path, a.TypeName, a.addressRow(true)})
	return string(data)
}

func (a *Artifacts) PrepareAddressPlans() {
	if !a.CompleteSchema || a.Selector.Paths != nil || len(a.Selector.Dimensions) != 0 || len(a.Selector.DimensionAlternatives) != 0 || len(a.Selector.ExcludedURIs) != 0 || len(a.Selector.CollectionPaths) != 0 {
		return
	}
	rows := make([]artifact.AddressRow, len(a.Entries))
	for i, entry := range a.Entries {
		rows[i] = entry.addressRow(true)
	}
	typed := artifact.NewAddressIndex(rows, true)
	var untyped *artifact.AddressIndex
	if a.Selector.Types == nil {
		for i := range rows {
			rows[i].Type = "*"
		}
		untyped = artifact.NewAddressIndex(rows, true)
	}
	names := a.displayNames()
	for _, entry := range a.Entries {
		entry.addressPlans = &artifactAddressPlans{}
		namesOwned := false
		completeType := a.Selector.Types == nil || !slices.ContainsFunc(a.AllPaths, func(path artifact.Path) bool {
			typ := strings.TrimPrefix(path.Type, "type:")
			return ArtifactTypeName(typ) == ArtifactTypeName(entry.TypeName) && !slices.Contains(a.Selector.Types, typ)
		})
		if completeType {
			if selectors, ok := typed.Selectors(entry.addressRow(true)); ok {
				entry.addressPlans.Typed = &artifactAddressPlan{Selectors: selectors}
			}
		}
		if untyped != nil {
			if selectors, ok := untyped.Selectors(entry.addressRow(false)); ok {
				entry.addressPlans.Untyped = &artifactAddressPlan{Selectors: selectors}
			}
		}
		for _, plan := range []*artifactAddressPlan{entry.addressPlans.Typed, entry.addressPlans.Untyped} {
			if plan == nil {
				continue
			}
			for _, selector := range plan.Selectors {
				if selector.Absent && names[selector.Dimension] != "" {
					if !namesOwned {
						entry.DimensionNames = maps.Clone(entry.DimensionNames)
						namesOwned = true
					}
					if entry.DimensionNames == nil {
						entry.DimensionNames = map[string]string{}
					}
					entry.DimensionNames[selector.Dimension] = names[selector.Dimension]
				}
			}
		}
	}
}

func (a *Artifact) HasAddressPlan(typed bool) bool {
	return a.addressPlan(typed) != nil
}
func (a *Artifact) addressPlan(typed bool) *artifactAddressPlan {
	if a.addressPlans == nil {
		return nil
	}
	if typed {
		return a.addressPlans.Typed
	}
	return a.addressPlans.Untyped
}

func (a *Artifact) WithAddressPlan(source *Artifact) *Artifact {
	result := a.Clone()
	result.addressPlans = source.addressPlans
	result.DimensionNames = source.DimensionNames
	return result
}

func (a *Artifact) HasCompleteAddressSchema() bool {
	return a.links != nil && (a.links.CompleteSchema || a.links.Typed != nil && !a.links.Typed.ExactDimensions)
}
