package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
)

const ArtifactTypeDimension = "type"

type Artifacts struct {
	Dimensions []*ArtifactDimension `field:"true" doc:"Ordered filterable dimensions for the current scope."`
	rows       []*Artifact
	// rowKeys dedups rows by coordinate tuple during construction. Two
	// occurrences of the same collection produce one row per item.
	rowKeys map[string]struct{}
}

func (*Artifacts) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Artifacts",
		NonNull:   true,
	}
}

func (*Artifacts) TypeDescription() string {
	return "A scoped, filterable view over workspace artifacts."
}

type ArtifactDimension struct {
	Name    string   `field:"true" doc:"Filter name as used in CLI flags and table headers."`
	KeyType *TypeDef `field:"true" doc:"Type of this dimension's keys."`
}

func (*ArtifactDimension) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ArtifactDimension",
		NonNull:   true,
	}
}

func (*ArtifactDimension) TypeDescription() string {
	return "A filterable axis of the artifact graph."
}

type Artifact struct {
	coordinates         []*string
	selectors           []dagql.Selector
	collectionSelectors []dagql.Selector
	scope               *Artifacts
}

func (*Artifact) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Artifact",
		NonNull:   true,
	}
}

func (*Artifact) TypeDescription() string {
	return "One artifact in a workspace artifact scope."
}

// NewWorkspaceArtifacts builds the root artifact scope for the given workspace
// modules.
//
// Dimensions and top-level rows are always discovered statically from
// typedefs: no user code runs. When enumerateItems is true, collection keys
// are additionally resolved by querying each module on its own standalone
// dagql server (the namespaced constructor is not served on the client's root
// schema for entrypoint modules). Per-module enumeration failures degrade to
// a warning rather than failing the whole scope.
func NewWorkspaceArtifacts(ctx context.Context, mods []dagql.ObjectResult[*Module], enumerateItems bool) (*Artifacts, error) {
	artifacts := &Artifacts{
		Dimensions: []*ArtifactDimension{
			{
				Name:    ArtifactTypeDimension,
				KeyType: &TypeDef{Kind: TypeDefKindString},
			},
		},
		rowKeys: map[string]struct{}{},
	}

	type pendingEnumeration struct {
		mod dagql.ObjectResult[*Module]
		obj *ObjectTypeDef
	}
	var toEnumerate []pendingEnumeration

	for _, mod := range mods {
		if mod.Self() == nil {
			continue
		}
		typeDef, ok := mod.Self().mainObjectTypeDefResult()
		if !ok || typeDef.Self() == nil || !typeDef.Self().AsObject.Valid || typeDef.Self().AsObject.Value.Self() == nil {
			slog.Warn("artifacts: skipping module without a main object", "module", mod.Self().Name())
			continue
		}
		obj := typeDef.Self().AsObject.Value.Self()
		rootSelector := dagql.Selector{Field: gqlFieldName(mod.Self().Name())}
		artifacts.addArtifactRow(artifactTypeName(obj), nil, []dagql.Selector{rootSelector}, nil)

		// Static pass: register dimensions for every reachable collection
		// without executing any user code.
		static := &artifactWalk{artifacts: artifacts}
		seen := map[string]struct{}{obj.Name: {}}
		if err := static.walkObject(ctx, mod.Self(), obj, []dagql.Selector{rootSelector}, nil, seen); err != nil {
			slog.Warn("artifacts: skipping module with invalid type graph", "module", mod.Self().Name(), "error", err)
			continue
		}
		if !enumerateItems || static.collections == 0 {
			continue
		}
		if obj.Constructor.Valid && functionRequiresArgs(obj.Constructor.Value.Self()) {
			slog.Warn("artifacts: skipping collection item enumeration; module constructor requires arguments", "module", mod.Self().Name())
			continue
		}
		toEnumerate = append(toEnumerate, pendingEnumeration{mod: mod, obj: obj})
	}

	for _, pending := range toEnumerate {
		modName := pending.mod.Self().Name()
		dag, err := dagqlServerForModule(ctx, pending.mod)
		if err != nil {
			slog.Warn("artifacts: skipping collection item enumeration; module server failed", "module", modName, "error", err)
			continue
		}
		dynamic := &artifactWalk{artifacts: artifacts, dag: dag}
		rootSelector := dagql.Selector{Field: gqlFieldName(modName)}
		seen := map[string]struct{}{pending.obj.Name: {}}
		if err := dynamic.walkObject(ctx, pending.mod.Self(), pending.obj, []dagql.Selector{rootSelector}, nil, seen); err != nil {
			slog.Warn("artifacts: skipping collection item enumeration; keys resolution failed", "module", modName, "error", err)
			continue
		}
	}

	sort.SliceStable(artifacts.rows, func(i, j int) bool { return artifactRowLess(artifacts.rows[i], artifacts.rows[j]) })

	return artifacts, nil
}

func (a *Artifacts) ensureDimension(name string, keyType *TypeDef) {
	if _, ok := a.dimensionIndex(name); ok {
		return
	}
	a.Dimensions = append(a.Dimensions, &ArtifactDimension{
		Name:    name,
		KeyType: keyType,
	})
}

// artifactWalk traverses a module's type graph rooted at its main object.
// With dag == nil it is a static discovery pass: it registers collection
// dimensions without executing any user code. With dag set, it additionally
// enumerates collection keys and adds item rows.
type artifactWalk struct {
	artifacts *Artifacts
	dag       *dagql.Server
	// collections counts collection occurrences found during the walk.
	collections int
}

// resolveArtifactObjectType resolves an object type definition within the
// given module. Module functions cannot return types defined in dependency
// modules, so a module's reachable object graph is always self-contained.
func resolveArtifactObjectType(mod *Module, typeDef *TypeDef) (*ObjectTypeDef, *CollectionTypeDef, bool) {
	if mod == nil || typeDef == nil || typeDef.Kind != TypeDefKindObject || !typeDef.AsObject.Valid || typeDef.AsObject.Value.Self() == nil {
		return nil, nil, false
	}
	for _, def := range mod.ObjectDefs {
		defSelf := def.Self()
		if defSelf == nil || !defSelf.AsObject.Valid || defSelf.AsObject.Value.Self() == nil {
			continue
		}
		if defSelf.AsObject.Value.Self().Name == typeDef.AsObject.Value.Self().Name {
			var collection *CollectionTypeDef
			if defSelf.AsCollection.Valid {
				collection = defSelf.AsCollection.Value.Self()
			}
			return defSelf.AsObject.Value.Self(), collection, true
		}
	}
	return nil, nil, false
}

func (w *artifactWalk) walkObject(
	ctx context.Context,
	mod *Module,
	obj *ObjectTypeDef,
	selectors []dagql.Selector,
	coordinates map[string]string,
	seen map[string]struct{},
) error {
	for _, field := range obj.Fields {
		fieldSelf := field.Self()
		if fieldSelf == nil {
			continue
		}
		memberSelectors := appendArtifactSelector(selectors, dagql.Selector{Field: fieldSelf.Name})
		if err := w.walkMember(ctx, mod, fieldSelf.TypeDef.Self(), memberSelectors, coordinates, seen); err != nil {
			return fmt.Errorf("artifact field %s.%s: %w", obj.Name, fieldSelf.Name, err)
		}
	}
	for _, fn := range obj.Functions {
		fnSelf := fn.Self()
		if fnSelf == nil || functionRequiresArgs(fnSelf) {
			continue
		}
		memberSelectors := appendArtifactSelector(selectors, dagql.Selector{Field: fnSelf.Name})
		if err := w.walkMember(ctx, mod, fnSelf.ReturnType.Self(), memberSelectors, coordinates, seen); err != nil {
			return fmt.Errorf("artifact function %s.%s: %w", obj.Name, fnSelf.Name, err)
		}
	}
	return nil
}

func (w *artifactWalk) walkMember(
	ctx context.Context,
	mod *Module,
	typeDef *TypeDef,
	selectors []dagql.Selector,
	coordinates map[string]string,
	seen map[string]struct{},
) error {
	objDef, collection, ok := resolveArtifactObjectType(mod, typeDef)
	if !ok {
		return nil
	}
	if collection != nil {
		w.collections++
		return w.walkCollection(ctx, mod, selectors, collection, coordinates, seen)
	}
	if _, ok := seen[objDef.Name]; ok {
		return nil
	}
	nextSeen := maps.Clone(seen)
	nextSeen[objDef.Name] = struct{}{}
	return w.walkObject(ctx, mod, objDef, selectors, coordinates, nextSeen)
}

func (w *artifactWalk) walkCollection(
	ctx context.Context,
	mod *Module,
	collectionSelectors []dagql.Selector,
	collection *CollectionTypeDef,
	coordinates map[string]string,
	seen map[string]struct{},
) error {
	if collection == nil || !collection.KeyType.Valid || !collection.ValueType.Valid {
		return nil
	}
	dimName, ok := collectionItemDimension(collection)
	if !ok {
		return nil
	}
	w.artifacts.ensureDimension(dimName, collection.KeyType.Value.Self())

	valueObj, _, valueFound := resolveArtifactObjectType(mod, collection.ValueType.Value.Self())
	if valueFound {
		// Bound re-entry: a value type that reaches its own collection again
		// (directly or via structure) would re-enumerate per item.
		if _, reentered := seen[valueObj.Name]; reentered {
			return nil
		}
	}

	if w.dag == nil {
		// Static pass: descend once into the value type to register nested
		// dimensions; items are not enumerated.
		if !valueFound {
			return nil
		}
		nextSeen := maps.Clone(seen)
		nextSeen[valueObj.Name] = struct{}{}
		return w.walkObject(ctx, mod, valueObj, collectionSelectors, coordinates, nextSeen)
	}

	var keys dagql.AnyResult
	keySelectors := appendArtifactSelector(collectionSelectors, dagql.Selector{Field: collectionKeysFieldName})
	if err := w.dag.Select(ctx, w.dag.Root(), &keys, keySelectors...); err != nil {
		return err
	}
	values, err := artifactCollectionKeys(keys)
	if err != nil {
		return err
	}
	for _, value := range values {
		nextCoordinates := maps.Clone(coordinates)
		if nextCoordinates == nil {
			nextCoordinates = map[string]string{}
		}
		getSelector := dagql.Selector{
			Field: collectionGetFunctionName,
			Args:  []dagql.NamedInput{{Name: collectionGetArgName, Value: value.input}},
		}
		itemSelectors := appendArtifactSelector(collectionSelectors, getSelector)
		nextCoordinates[dimName] = value.coordinate
		w.artifacts.addArtifactRow(dimName, nextCoordinates, itemSelectors, collectionSelectors)

		if !valueFound {
			continue
		}
		nextSeen := maps.Clone(seen)
		nextSeen[valueObj.Name] = struct{}{}
		if err := w.walkObject(ctx, mod, valueObj, itemSelectors, nextCoordinates, nextSeen); err != nil {
			return fmt.Errorf("artifact collection item %s=%s: %w", dimName, value.coordinate, err)
		}
	}
	return nil
}

func (a *Artifacts) addArtifactRow(
	artifactType string,
	coordinates map[string]string,
	selectors []dagql.Selector,
	collectionSelectors []dagql.Selector,
) {
	coords := make([]*string, len(a.Dimensions))
	coords[0] = ptr(artifactType)
	for dimension, value := range coordinates {
		if dimIdx, ok := a.dimensionIndex(dimension); ok {
			coords[dimIdx] = ptr(value)
		}
	}
	if a.rowKeys != nil {
		key := artifactRowKey(coords)
		if _, dup := a.rowKeys[key]; dup {
			return
		}
		a.rowKeys[key] = struct{}{}
	}
	a.rows = append(a.rows, &Artifact{
		coordinates:         coords,
		selectors:           cloneArtifactSelectors(selectors),
		collectionSelectors: cloneArtifactSelectors(collectionSelectors),
	})
}

func artifactRowKey(coords []*string) string {
	var key strings.Builder
	for _, coord := range coords {
		if coord == nil {
			key.WriteString("\x00~")
			continue
		}
		key.WriteString("\x00=")
		key.WriteString(*coord)
	}
	return key.String()
}

func collectionItemDimension(collection *CollectionTypeDef) (string, bool) {
	if collection == nil || !collection.ValueType.Valid {
		return "", false
	}
	valueType := collection.ValueType.Value.Self()
	if valueType == nil || valueType.Kind != TypeDefKindObject || !valueType.AsObject.Valid || valueType.AsObject.Value.Self() == nil {
		return "", false
	}
	return artifactTypeName(valueType.AsObject.Value.Self()), true
}

// artifactTypeName derives a dimension/type coordinate from an object type's
// GraphQL name, kebab-cased. Module namespacing keeps these workspace-unique
// by construction: module "go" defining type Test yields "go-test", while
// two modules defining the same local type name cannot collide.
func artifactTypeName(obj *ObjectTypeDef) string {
	return strcase.ToKebab(obj.Name)
}

type artifactCollectionKey struct {
	input      dagql.Input
	coordinate string
}

func artifactCollectionKeys(keys dagql.AnyResult) ([]artifactCollectionKey, error) {
	list, ok := dagql.UnwrapAs[dagql.Enumerable](keys)
	if !ok {
		return nil, fmt.Errorf("collection keys resolved to %T, not a list", keys)
	}
	values := make([]artifactCollectionKey, 0, list.Len())
	for i := 1; i <= list.Len(); i++ {
		item, err := list.Nth(i)
		if err != nil {
			return nil, err
		}
		input, ok := item.(dagql.Input)
		if !ok {
			return nil, fmt.Errorf("collection key resolved to %T, not an input", item)
		}
		value, err := artifactCollectionKeyValue(item)
		if err != nil {
			return nil, err
		}
		values = append(values, artifactCollectionKey{
			input:      input,
			coordinate: value,
		})
	}
	return values, nil
}

func appendArtifactSelector(selectors []dagql.Selector, selector dagql.Selector) []dagql.Selector {
	next := make([]dagql.Selector, 0, len(selectors)+1)
	next = append(next, selectors...)
	next = append(next, selector)
	return next
}

func cloneArtifactSelectors(selectors []dagql.Selector) []dagql.Selector {
	if len(selectors) == 0 {
		return nil
	}
	cloned := make([]dagql.Selector, len(selectors))
	for i, selector := range selectors {
		cloned[i] = selector
		cloned[i].Args = append([]dagql.NamedInput(nil), selector.Args...)
	}
	return cloned
}

func artifactCollectionKeyValue(value dagql.Typed) (string, error) {
	switch value := value.(type) {
	case dagql.String:
		return value.String(), nil
	case dagql.Int:
		return strconv.FormatInt(value.Int64(), 10), nil
	case dagql.Float:
		return strconv.FormatFloat(float64(value), 'f', -1, 64), nil
	case dagql.Boolean:
		return strconv.FormatBool(bool(value)), nil
	case *dagql.EnumValueName:
		return value.Name, nil
	case *ModuleEnum:
		return value.Name, nil
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(payload), nil
	}
}

func artifactRowLess(left, right *Artifact) bool {
	maxLen := len(left.coordinates)
	if len(right.coordinates) > maxLen {
		maxLen = len(right.coordinates)
	}
	for i := 0; i < maxLen; i++ {
		var leftVal, rightVal string
		if i < len(left.coordinates) && left.coordinates[i] != nil {
			leftVal = *left.coordinates[i]
		}
		if i < len(right.coordinates) && right.coordinates[i] != nil {
			rightVal = *right.coordinates[i]
		}
		if leftVal != rightVal {
			return leftVal < rightVal
		}
	}
	return false
}

func (a *Artifacts) dimensionIndex(name string) (int, bool) {
	for i, dim := range a.Dimensions {
		if dim != nil && dim.Name == name {
			return i, true
		}
	}
	return -1, false
}

func (a *Artifacts) FilterDimension(dimension string) (*Artifacts, error) {
	idx, ok := a.dimensionIndex(dimension)
	if !ok {
		return nil, fmt.Errorf("artifact dimension %q is not present in this scope", dimension)
	}

	filtered := &Artifacts{Dimensions: a.Dimensions}
	for _, row := range a.rows {
		if idx < len(row.coordinates) && row.coordinates[idx] != nil {
			filtered.rows = append(filtered.rows, row)
		}
	}
	return filtered, nil
}

func (a *Artifacts) FilterCoordinates(dimension string, values []string) (*Artifacts, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("artifact coordinate filter for %q requires at least one value", dimension)
	}

	idx, ok := a.dimensionIndex(dimension)
	if !ok {
		return nil, fmt.Errorf("artifact dimension %q is not present in this scope", dimension)
	}

	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}

	filtered := &Artifacts{Dimensions: a.Dimensions}
	for _, row := range a.rows {
		if idx >= len(row.coordinates) || row.coordinates[idx] == nil {
			continue
		}
		if _, ok := allowed[*row.coordinates[idx]]; ok {
			filtered.rows = append(filtered.rows, row)
		}
	}
	return filtered, nil
}

func (a *Artifacts) Items() []*Artifact {
	items := make([]*Artifact, len(a.rows))
	for i, row := range a.rows {
		// Rows added before later dimensions were registered are shorter
		// than the final dimension set: pad so every coordinate row has the
		// same length and order as scope.dimensions.
		coords := row.coordinates
		if len(coords) < len(a.Dimensions) {
			padded := make([]*string, len(a.Dimensions))
			copy(padded, coords)
			coords = padded
		}
		items[i] = &Artifact{
			coordinates:         coords,
			selectors:           row.selectors,
			collectionSelectors: row.collectionSelectors,
			scope:               a,
		}
	}
	return items
}

func (a *Artifact) Coordinates() []*string {
	if a == nil {
		return nil
	}
	coordinates := make([]*string, len(a.coordinates))
	for i, coord := range a.coordinates {
		if coord != nil {
			coordinates[i] = ptr(*coord)
		}
	}
	return coordinates
}

func (a *Artifact) Scope() *Artifacts {
	if a == nil {
		return nil
	}
	return a.scope
}

func (a *Artifact) Selectors() []dagql.Selector {
	if a == nil {
		return nil
	}
	return cloneArtifactSelectors(a.selectors)
}

func (a *Artifact) CollectionSelectors() []dagql.Selector {
	if a == nil {
		return nil
	}
	return cloneArtifactSelectors(a.collectionSelectors)
}

func (a *Artifact) Coordinate(name string) (string, bool) {
	if a == nil || a.scope == nil {
		return "", false
	}
	idx, ok := a.scope.dimensionIndex(name)
	if !ok || idx >= len(a.coordinates) || a.coordinates[idx] == nil {
		return "", false
	}
	return *a.coordinates[idx], true
}
