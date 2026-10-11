package dagql

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Aliases are hidden alternate names of an object or interface type, a field,
// or a field argument: old spellings that keep working for a while after a
// rename.
//
// Queries may use an alias wherever the canonical name is accepted, but no
// schema a client can read shows it: Schema, SchemaForView, introspection and
// everything generated from them (SDK codegen, the schema JSON file) only have
// the canonical name. Only validation sees aliases (see SchemaWithAliases),
// and a query that uses one is rewritten to the canonical name when it's
// parsed, so its calls, IDs and cache keys are the canonical call's.
//
// An alias that collides with a real name, or that two names claim, is
// ignored.
type Aliases struct {
	// Names are the hidden alternate names.
	Names []string
	// View limits the views in which the aliases are accepted. Nil accepts
	// them in every view.
	View ViewFilter
}

// namesIn returns the alias names accepted in view.
func (a Aliases) namesIn(view call.View) []string {
	if len(a.Names) == 0 {
		return nil
	}
	if a.View != nil && !a.View.Contains(view) {
		return nil
	}
	return a.Names
}

// typeAliased is implemented by object and interface types that can declare
// type aliases.
type typeAliased interface {
	TypeAliases() Aliases
}

// nameAliases indexes the aliases accepted in one view of a schema, each
// mapped to its canonical name.
type nameAliases struct {
	// Types maps a type alias to the canonical type name.
	Types map[string]string `json:"types,omitempty"`
	// Fields maps a type name to its field aliases.
	Fields map[string]map[string]string `json:"fields,omitempty"`
	// Args maps a type name and a canonical field name to its argument
	// aliases.
	Args map[string]map[string]map[string]string `json:"args,omitempty"`
}

func (idx *nameAliases) empty() bool {
	return idx == nil || (len(idx.Types) == 0 && len(idx.Fields) == 0 && len(idx.Args) == 0)
}

// typeName returns the canonical name of a type name that may be an alias.
func (idx *nameAliases) typeName(ctx context.Context, name string) string {
	if idx == nil || name == "" {
		return name
	}
	if canonical, ok := idx.Types[name]; ok {
		noteAliasUse(ctx, "type", "", name, canonical)
		return canonical
	}
	return name
}

// field returns astField with an aliased field name and aliased argument names
// replaced by their canonical names, or astField itself when it uses none.
// The response key stays the name the query used.
func (idx *nameAliases) field(ctx context.Context, typeName string, astField *ast.Field) (*ast.Field, error) {
	if idx.empty() {
		return astField, nil
	}
	name := astField.Name
	if canonical, ok := idx.Fields[typeName][name]; ok {
		noteAliasUse(ctx, "field", typeName, name, canonical)
		name = canonical
	}
	argAliases := idx.Args[typeName][name]
	if name == astField.Name && !slices.ContainsFunc(astField.Arguments, func(arg *ast.Argument) bool {
		_, ok := argAliases[arg.Name]
		return ok
	}) {
		return astField, nil
	}

	rewritten := *astField
	rewritten.Name = name
	if rewritten.Alias == "" {
		rewritten.Alias = astField.Name
	}
	if len(argAliases) == 0 {
		return &rewritten, nil
	}
	rewritten.Arguments = make(ast.ArgumentList, 0, len(astField.Arguments))
	given := make(map[string]string, len(astField.Arguments))
	for _, arg := range astField.Arguments {
		argName := arg.Name
		if canonical, ok := argAliases[argName]; ok {
			noteAliasUse(ctx, "argument", typeName+"."+name, argName, canonical)
			argName = canonical
		}
		if prev, ok := given[argName]; ok {
			return nil, fmt.Errorf("%s.%s: argument %q given twice, as %q and %q", typeName, name, argName, prev, arg.Name)
		}
		given[argName] = arg.Name
		if argName == arg.Name {
			rewritten.Arguments = append(rewritten.Arguments, arg)
			continue
		}
		renamed := *arg
		renamed.Name = argName
		rewritten.Arguments = append(rewritten.Arguments, &renamed)
	}
	return &rewritten, nil
}

// noteAliasUse records that a query used a deprecated alias: as an event on
// the current span, and in the debug log.
func noteAliasUse(ctx context.Context, kind, parent, alias, canonical string) {
	attrs := []attribute.KeyValue{
		attribute.String(telemetryattrs.DagAliasKindAttr, kind),
		attribute.String(telemetryattrs.DagAliasNameAttr, alias),
		attribute.String(telemetryattrs.DagAliasCanonicalAttr, canonical),
	}
	if parent != "" {
		attrs = append(attrs, attribute.String(telemetryattrs.DagAliasParentAttr, parent))
	}
	trace.SpanFromContext(ctx).AddEvent(telemetryattrs.DagAliasEvent, trace.WithAttributes(attrs...))
	slog.DebugContext(ctx, "deprecated alias used", "kind", kind, "parent", parent, "alias", alias, "canonical", canonical)
}

// aliasClaims collects the aliases of one scope (the types of a schema, the
// fields of a type, the arguments of a field), refusing an alias that two
// names claim.
type aliasClaims struct {
	scope   string
	claims  map[string]string
	refused map[string]bool
}

func (c *aliasClaims) claim(alias, canonical string) {
	if c.refused[alias] {
		return
	}
	if prev, ok := c.claims[alias]; ok {
		if prev == canonical {
			return
		}
		slog.Warn("ignoring ambiguous name alias", "scope", c.scope, "alias", alias, "names", []string{prev, canonical})
		delete(c.claims, alias)
		if c.refused == nil {
			c.refused = map[string]bool{}
		}
		c.refused[alias] = true
		return
	}
	if c.claims == nil {
		c.claims = map[string]string{}
	}
	c.claims[alias] = canonical
}

// collectNameAliasesLocked indexes the aliases of the object and interface
// types in schema, the server's schema for view. An alias that collides with a
// name of its scope is ignored. The caller holds installLock.
func (s *Server) collectNameAliasesLocked(view call.View, schema *ast.Schema) *nameAliases {
	idx := &nameAliases{}
	types := aliasClaims{scope: "types"}
	collect := func(name string, t any, specs []FieldSpec) {
		def := schema.Types[name]
		if def == nil {
			return
		}
		if aliased, ok := t.(typeAliased); ok {
			for _, alias := range aliased.TypeAliases().namesIn(view) {
				switch {
				case alias == name:
				case schema.Types[alias] != nil:
					slog.Warn("ignoring name alias that collides with a type", "type", name, "alias", alias)
				default:
					types.claim(alias, name)
				}
			}
		}
		fields := aliasClaims{scope: name}
		for _, spec := range specs {
			fieldDef := def.Fields.ForName(spec.Name)
			if fieldDef == nil {
				continue
			}
			for _, alias := range spec.Aliases.namesIn(view) {
				switch {
				case alias == spec.Name:
				case def.Fields.ForName(alias) != nil:
					slog.Warn("ignoring name alias that collides with a field", "type", name, "field", spec.Name, "alias", alias)
				default:
					fields.claim(alias, spec.Name)
				}
			}
			args := aliasClaims{scope: name + "." + spec.Name}
			for _, arg := range spec.Args.Inputs(view) {
				if arg.Internal || fieldDef.Arguments.ForName(arg.Name) == nil {
					continue
				}
				for _, alias := range arg.Aliases.namesIn(view) {
					switch {
					case alias == arg.Name:
					case fieldDef.Arguments.ForName(alias) != nil:
						slog.Warn("ignoring name alias that collides with an argument", "type", name, "field", spec.Name, "arg", arg.Name, "alias", alias)
					default:
						args.claim(alias, arg.Name)
					}
				}
			}
			if len(args.claims) > 0 {
				if idx.Args == nil {
					idx.Args = map[string]map[string]map[string]string{}
				}
				if idx.Args[name] == nil {
					idx.Args[name] = map[string]map[string]string{}
				}
				idx.Args[name][spec.Name] = args.claims
			}
		}
		if len(fields.claims) > 0 {
			if idx.Fields == nil {
				idx.Fields = map[string]map[string]string{}
			}
			idx.Fields[name] = fields.claims
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.objects)) {
		t := s.objects[name]
		if def := schema.Types[name]; def == nil || def.Kind != ast.Object {
			continue
		}
		collect(name, t, t.FieldSpecs(view))
	}
	for _, name := range slices.Sorted(maps.Keys(s.interfaces)) {
		iface := s.interfaces[name]
		if def := schema.Types[name]; def == nil || def.Kind != ast.Interface {
			continue
		}
		collect(name, iface, iface.FieldSpecs(view))
	}
	idx.Types = types.claims
	return idx
}

// schema returns base with the aliases added, each marked @deprecated with
// its canonical name: field aliases as copies of their field, argument
// aliases as copies of their argument, type aliases as copies of their type
// that match wherever it does. A required argument with aliases is optional
// in it, since a query may give it by either name; the call still requires it.
// Returns base itself when there are no aliases.
func (idx *nameAliases) schema(base *ast.Schema) *ast.Schema {
	if idx.empty() {
		return base
	}
	out := *base
	out.Types = maps.Clone(base.Types)

	replaced := map[*ast.Definition]*ast.Definition{}
	for name, def := range base.Types {
		if len(idx.Fields[name]) == 0 && len(idx.Args[name]) == 0 {
			continue
		}
		aliased := withFieldAliases(def, idx.Fields[name], idx.Args[name])
		out.Types[name] = aliased
		replaced[def] = aliased
	}
	if r, ok := replaced[out.Query]; ok {
		out.Query = r
	}
	out.PossibleTypes = make(map[string][]*ast.Definition, len(base.PossibleTypes)+len(idx.Types))
	for name, defs := range base.PossibleTypes {
		defs = slices.Clone(defs)
		for i, def := range defs {
			if r, ok := replaced[def]; ok {
				defs[i] = r
			}
		}
		out.PossibleTypes[name] = defs
	}
	for alias, canonical := range idx.Types {
		def := out.Types[canonical]
		aliasDef := *def
		aliasDef.Name = alias
		aliasDef.Directives = withAliasDeprecation(def.Directives, canonical)
		out.Types[alias] = &aliasDef
		// The alias stands for the canonical type: a fragment on it is valid
		// wherever one on the canonical type is.
		out.PossibleTypes[alias] = slices.Clone(out.PossibleTypes[canonical])
	}
	return &out
}

func withFieldAliases(def *ast.Definition, fieldAliases map[string]string, argAliases map[string]map[string]string) *ast.Definition {
	byField := aliasesByCanonical(fieldAliases)
	out := *def
	out.Fields = make(ast.FieldList, 0, len(def.Fields)+len(fieldAliases))
	for _, field := range def.Fields {
		if args := argAliases[field.Name]; len(args) > 0 {
			field = withArgAliases(field, args)
		}
		out.Fields = append(out.Fields, field)
		for _, alias := range byField[field.Name] {
			aliasField := *field
			aliasField.Name = alias
			aliasField.Directives = withAliasDeprecation(field.Directives, field.Name)
			out.Fields = append(out.Fields, &aliasField)
		}
	}
	return &out
}

func withArgAliases(field *ast.FieldDefinition, argAliases map[string]string) *ast.FieldDefinition {
	byArg := aliasesByCanonical(argAliases)
	out := *field
	out.Arguments = make(ast.ArgumentDefinitionList, 0, len(field.Arguments)+len(argAliases))
	for _, arg := range field.Arguments {
		aliases := byArg[arg.Name]
		if len(aliases) == 0 {
			out.Arguments = append(out.Arguments, arg)
			continue
		}
		canonical := *arg
		if canonical.Type.NonNull && canonical.DefaultValue == nil {
			optional := *canonical.Type
			optional.NonNull = false
			canonical.Type = &optional
		}
		out.Arguments = append(out.Arguments, &canonical)
		for _, alias := range aliases {
			aliasArg := canonical
			aliasArg.Name = alias
			aliasArg.Directives = withAliasDeprecation(arg.Directives, arg.Name)
			out.Arguments = append(out.Arguments, &aliasArg)
		}
	}
	return &out
}

// aliasesByCanonical inverts an alias -> canonical map, sorting the aliases.
func aliasesByCanonical(aliases map[string]string) map[string][]string {
	out := make(map[string][]string, len(aliases))
	for alias, canonical := range aliases {
		out[canonical] = append(out[canonical], alias)
	}
	for _, names := range out {
		slices.Sort(names)
	}
	return out
}

// withAliasDeprecation returns the directives of an alias of canonical: the
// canonical name's, marked @deprecated unless they already are.
func withAliasDeprecation(dirs ast.DirectiveList, canonical string) ast.DirectiveList {
	if dirs.ForName("deprecated") != nil {
		return dirs
	}
	reason := fmt.Sprintf("Use `%s` instead.", canonical)
	return append(slices.Clone(dirs), deprecated(&reason))
}
