package core

import (
	"encoding/json"
	"maps"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
)

// A non-nil plan can be empty: the type alone can identify an artifact.
type artifactAddressPlan struct{ Selectors []dagaddress.Pair }
type artifactAddressPlans struct{ Typed, Untyped *artifactAddressPlan }

func (a *Artifact) addressRow(typed bool) artifact.AddressRow {
	typ := "*"
	if typed {
		typ = a.TypeName
	}
	return artifact.AddressRow{Type: typ, Coordinates: a.CanonicalCoordinates()}
}

// AddressIdentity identifies an artifact within its source workspace. It does
// not depend on a selection or the shorter spelling of a link.
func (a *Artifact) AddressIdentity() string {
	data, _ := json.Marshal(struct {
		Path []string
		Row  artifact.AddressRow
	}{a.Path, a.addressRow(true)})
	return string(data)
}

// PrepareAddressPlans requires an expanded catalog with no row filters.
// A type filter still supplies a complete catalog for typed addresses.
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
		if selectors, ok := typed.Selectors(entry.addressRow(true)); ok {
			entry.addressPlans.Typed = &artifactAddressPlan{Selectors: selectors}
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

// WithAddressPlan retains the source artifact's evaluation and display context.
func (a *Artifact) WithAddressPlan(source *Artifact) *Artifact {
	result := a.Clone()
	result.addressPlans = source.addressPlans
	result.DimensionNames = source.DimensionNames
	return result
}

// A deliberately narrow schema must not load unrelated modules for shorter links.
func (a *Artifact) HasCompleteAddressSchema() bool {
	return a.links != nil && (a.links.CompleteSchema || a.links.Typed != nil && !a.links.Typed.ExactDimensions)
}
