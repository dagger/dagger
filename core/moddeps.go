package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
	"golang.org/x/exp/slices"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"

	ModuleName = "daggercore"
)

/*
ModDeps represents a set of dependencies for a module or for a caller depending on a
particular set of modules to be served.
*/
type ModDeps struct {
	Mods []Mod // TODO hide
}

func NewModDeps(mods []Mod) *ModDeps {
	slices.SortFunc(mods, func(a, b Mod) int {
		return strings.Compare(a.Digest().String(), b.Digest().String())
	})
	mods = slices.CompactFunc(mods, func(a, b Mod) bool {
		return a.Digest() == b.Digest()
	})
	return &ModDeps{
		Mods: mods,
	}
}

func (d *ModDeps) Append(mods ...Mod) *ModDeps {
	deps := append([]Mod{}, d.Mods...)
	deps = append(deps, mods...)
	return NewModDeps(deps)
}

// The combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) Install(ctx context.Context, dag *dagql.Server) error {
	var objects []*ModuleObjectType
	var ifaces []*InterfaceType
	for _, mod := range d.Mods {
		err := mod.Install(ctx, dag)
		if err != nil {
			return fmt.Errorf("failed to get schema for module %q: %w", mod.Name(), err)
		}

		// TODO support core interfaces types
		if userMod, ok := mod.(*Module); ok {
			defs, err := mod.TypeDefs(ctx)
			if err != nil {
				return fmt.Errorf("failed to get type defs for module %q: %w", mod.Name(), err)
			}
			for _, def := range defs {
				switch def.Kind {
				case TypeDefKindObject:
					objects = append(objects, &ModuleObjectType{
						typeDef: def.AsObject.Value,
						mod:     userMod,
					})
				case TypeDefKindInterface:
					ifaces = append(ifaces, &InterfaceType{
						typeDef: def.AsInterface.Value,
						mod:     userMod,
					})
				}
			}
		}
	}

	// add any extensions to objects for the interfaces they implement (if any)
	for _, objType := range objects {
		obj := objType.typeDef
		class, found := dag.ObjectType(obj.Name)
		if !found {
			return fmt.Errorf("failed to find object %q in schema", obj.Name)
		}
		for _, ifaceType := range ifaces {
			iface := ifaceType.typeDef
			if !obj.IsSubtypeOf(iface) {
				continue
			}
			objType := objType
			ifaceType := ifaceType
			asIfaceFieldName := gqlFieldName(fmt.Sprintf("as%s", iface.Name))
			class.Extend(
				dagql.FieldSpec{
					Name:        asIfaceFieldName,
					Description: fmt.Sprintf("Converts this %s to a %s.", obj.Name, iface.Name),
					Type:        &InterfaceAnnotatedValue{TypeDef: iface},
					Module:      ifaceType.mod.IDModule(),
				},
				func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
					inst, ok := self.(dagql.Instance[*ModuleObject])
					if !ok {
						return nil, fmt.Errorf("expected %T to be a ModuleObject", self)
					}
					return &InterfaceAnnotatedValue{
						TypeDef:        iface,
						Fields:         inst.Self.Fields,
						UnderlyingType: objType,
						IfaceType:      ifaceType,
					}, nil
				},
			)
		}
	}

	return nil
}

// All the TypeDefs exposed by this set of dependencies
func (d *ModDeps) TypeDefs(ctx context.Context) ([]*TypeDef, error) {
	var typeDefs []*TypeDef
	for _, mod := range d.Mods {
		modTypeDefs, err := mod.TypeDefs(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get objects from mod %q: %w", mod.Name(), err)
		}
		typeDefs = append(typeDefs, modTypeDefs...)
	}
	return typeDefs, nil
}

// Search the deps for the given type def, returning the ModType if found. This does not recurse
// to transitive dependencies; it only returns types directly exposed by the schema of the top-level
// deps.
func (d *ModDeps) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error) {
	for _, mod := range d.Mods {
		modType, ok, err := mod.ModTypeFor(ctx, typeDef, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get type from mod %q: %w", mod.Name(), err)
		}
		if !ok {
			continue
		}
		return modType, true, nil
	}
	return nil, false, nil
}

// Return a unique digest for this set of dependencies, based on the module digests
func (d *ModDeps) Digest() digest.Digest {
	dgsts := make([]string, len(d.Mods))
	for i, mod := range d.Mods {
		dgsts[i] = mod.Digest().String()
	}
	return digest.FromString(strings.Join(dgsts, " "))
}
