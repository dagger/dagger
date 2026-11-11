package core

import (
	"encoding/json"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
)

// A non-nil key set can be empty: the type alone can identify an artifact.
type artifactAddressKeys struct{ Keys []flattening.Coordinate }
type artifactAddressPlans struct{ Typed, Untyped *artifactAddressKeys }

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
	data, _ := json.Marshal(a.addressRow(true))
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
	for _, entry := range a.Entries {
		entry.addressPlans = &artifactAddressPlans{}
		if keys, ok := typed.Keys(entry.addressRow(true)); ok {
			entry.addressPlans.Typed = &artifactAddressKeys{Keys: keys}
		}
		if untyped != nil {
			if keys, ok := untyped.Keys(entry.addressRow(false)); ok {
				entry.addressPlans.Untyped = &artifactAddressKeys{Keys: keys}
			}
		}
	}
}

func (a *Artifact) HasAddressPlan(typed bool) bool {
	return a.addressKeys(typed) != nil
}
func (a *Artifact) addressKeys(typed bool) *artifactAddressKeys {
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
	return a.links != nil && a.links.Typed != nil && !a.links.Typed.ExactDimensions
}
