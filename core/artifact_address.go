package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
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
	return a.links != nil && a.links.CompleteSchema
}

// ArtifactAddressPlanner plans addresses against a whole selection, as
// PrepareAddressPlans does over its expansion, but expands a collection only
// when its items could change the plan asked for. A link is the same either
// way; a plan that needs no collection costs no collection call.
//
// A planner is safe for concurrent use: callers planning many artifacts of
// one selection share it, and each expansion's rows join its index once.
type ArtifactAddressPlanner struct {
	source    dagql.ObjectResult[*Artifacts]
	selection *Artifacts
	typed     bool
	names     map[string]string
	index     *artifact.AddressIndex
	// discoveries holds each template's rows once a caller has read them.
	discoveries sync.Map
}

// NewArtifactAddressPlanner plans the typed or untyped addresses of selection,
// which is unexpanded. Like PrepareAddressPlans it plans nothing for a
// filtered selection, and untyped addresses only for a selection of all types.
func NewArtifactAddressPlanner(source dagql.ObjectResult[*Artifacts], typed bool) (*ArtifactAddressPlanner, error) {
	bound, err := source.Self().SchemaSelection()
	if err != nil {
		return nil, err
	}
	planner := &ArtifactAddressPlanner{source: source, selection: bound, typed: typed}
	if !bound.CompleteSchema || bound.Selector.Paths != nil || len(bound.Selector.Dimensions) != 0 || len(bound.Selector.DimensionAlternatives) != 0 || len(bound.Selector.ExcludedURIs) != 0 || len(bound.Selector.CollectionPaths) != 0 || !typed && bound.Selector.Types != nil {
		return planner, nil
	}
	planner.names = bound.displayNames()
	// The collection dimensions of every entry at each path: entries can
	// share a path, and a template's listing must exclude the others.
	pathDimensions := map[string]map[string]bool{}
	for _, entry := range bound.Entries {
		key := strings.Join(entry.Path, "/")
		if pathDimensions[key] == nil {
			pathDimensions[key] = map[string]bool{}
		}
		dimensions := entry.unexpandedCollections()
		for _, dimension := range dimensions {
			pathDimensions[key][dimension] = true
		}
	}
	var rows []artifact.AddressRow
	var templates []*artifact.AddressTemplate
	for _, entry := range bound.Entries {
		row := entry.addressRow(typed)
		dimensions := entry.unexpandedCollections()
		if len(dimensions) == 0 {
			rows = append(rows, row)
			continue
		}
		template := artifactTemplate{path: slices.Clone(entry.Path), dimensions: slices.Sorted(slices.Values(dimensions))}
		for dimension := range pathDimensions[strings.Join(entry.Path, "/")] {
			if !slices.Contains(template.dimensions, dimension) {
				template.excluded = append(template.excluded, dimension)
			}
		}
		slices.Sort(template.excluded)
		templates = append(templates, &artifact.AddressTemplate{
			Type: row.Type, Coordinates: row.Coordinates, Dimensions: dimensions,
			Discover: func(ctx context.Context) ([]artifact.AddressRow, error) {
				key := template.key()
				if rows, ok := planner.discoveries.Load(key); ok {
					return rows.([]artifact.AddressRow), nil
				}
				rows, err := planner.discover(ctx, template)
				if err == nil {
					planner.discoveries.Store(key, rows)
				}
				return rows, err
			},
		})
	}
	planner.index = artifact.NewLazyAddressIndex(rows, templates)
	return planner, nil
}

// unexpandedCollections are the collection dimensions whose keys an
// expansion of the artifact would discover.
func (a *Artifact) unexpandedCollections() []string {
	var dimensions []string
	for node := a.Node; node != nil; node = node.Parent {
		if node.CollectionDimension != nil && node.CollectionKey == nil && !artifact.IsStaticDimension(node.CollectionDimension.CollectionType) {
			dimensions = append(dimensions, node.CollectionDimension.CollectionType)
		}
	}
	return dimensions
}

// artifactTemplate names one unexpanded entry of a planner's selection: its
// path, which entries can share, and its collection dimensions, which tell
// them apart. excluded are the other entries' dimensions at its path.
type artifactTemplate struct {
	path       []string
	dimensions []string
	excluded   []string
}

func (t artifactTemplate) key() string {
	return strings.Join(t.path, "/") + "?" + strings.Join(t.dimensions, "&")
}

// discover lists the template's artifacts, as a listing of the whole
// selection would expand them. The listing is a DagQL call of the caller's
// own, so the cache decides when concurrent and later callers share it, as it
// did for the whole selection's listing.
func (p *ArtifactAddressPlanner) discover(ctx context.Context, template artifactTemplate) ([]artifact.AddressRow, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	paths := make(dagql.ArrayInput[dagql.String], len(template.path))
	for i, field := range template.path {
		paths[i] = dagql.String(field)
	}
	// Select the template alone by its shape: every one of its dimensions,
	// and none of the others at its path, so no other entry is expanded. An
	// exact path selects a collection's item as the collection itself unless
	// its dimension is selected, which this also does.
	selectors := []dagql.Selector{{Field: "filterPath", Args: []dagql.NamedInput{{Name: "path", Value: paths}}}}
	for _, dimension := range template.dimensions {
		selectors = append(selectors, dagql.Selector{Field: "filterDimensions", Args: []dagql.NamedInput{{Name: "dimensions", Value: dagql.ArrayInput[dagql.String]{dagql.String(dimension)}}}})
	}
	if len(template.excluded) > 0 {
		excluded := make(dagql.ArrayInput[dagql.String], len(template.excluded))
		for i, dimension := range template.excluded {
			excluded[i] = dagql.String(dimension)
		}
		selectors = append(selectors, dagql.Selector{Field: "filterDimensions", Args: []dagql.NamedInput{{Name: "dimensions", Value: excluded}, {Name: "exclude", Value: dagql.Boolean(true)}}})
	}
	selectors = append(selectors, dagql.Selector{Field: "items", View: srv.View})
	var list dagql.AnyResult
	if err := srv.Select(ctx, p.source, &list, selectors...); err != nil {
		return nil, err
	}
	// Read the items from the list's payload, without attaching each one.
	items, ok := list.Unwrap().(dagql.Enumerable)
	if !ok {
		return nil, fmt.Errorf("artifact items: unexpected %T", list.Unwrap())
	}
	var rows []artifact.AddressRow
	for nth := 1; nth <= items.Len(); nth++ {
		value, err := items.Nth(nth)
		if err != nil {
			return nil, err
		}
		item, ok := dagql.UnwrapAs[*Artifact](value)
		if !ok {
			return nil, fmt.Errorf("artifact items: unexpected %T", value)
		}
		// Keep only this template's rows: entries sharing its path differ in
		// their collection dimensions.
		if slices.Equal(item.Path, template.path) && slices.Equal(item.collectionDimensions(), template.dimensions) {
			rows = append(rows, item.addressRow(p.typed))
		}
	}
	return rows, nil
}

// collectionDimensions are the sorted collection dimensions of the
// artifact's coordinates.
func (a *Artifact) collectionDimensions() []string {
	var dimensions []string
	for _, coordinate := range a.CanonicalCoordinates() {
		if !artifact.IsStaticDimension(coordinate.Dimension) {
			dimensions = append(dimensions, coordinate.Dimension)
		}
	}
	slices.Sort(dimensions)
	return dimensions
}

// Plan returns target's address plan as a source for WithAddressPlan, or nil
// when the selection gives it none.
func (p *ArtifactAddressPlanner) Plan(ctx context.Context, target *Artifact) (*Artifact, error) {
	if p.index == nil {
		return nil, nil
	}
	if p.typed && p.selection.Selector.Types != nil && slices.ContainsFunc(p.selection.AllPaths, func(path artifact.Path) bool {
		typ := strings.TrimPrefix(path.Type, "type:")
		return ArtifactTypeName(typ) == ArtifactTypeName(target.TypeName) && !slices.Contains(p.selection.Selector.Types, typ)
	}) {
		return nil, nil
	}
	selectors, ok, err := p.index.PlanSelectors(ctx, target.addressRow(p.typed))
	if err != nil || !ok {
		return nil, err
	}
	plan := &artifactAddressPlan{Selectors: selectors}
	plans := &artifactAddressPlans{Untyped: plan}
	if p.typed {
		plans = &artifactAddressPlans{Typed: plan}
	}
	names := dimensionNames(p.names, target)
	for _, selector := range selectors {
		if selector.Absent && p.names[selector.Dimension] != "" {
			names[selector.Dimension] = p.names[selector.Dimension]
		}
	}
	return &Artifact{addressPlans: plans, DimensionNames: names}, nil
}
