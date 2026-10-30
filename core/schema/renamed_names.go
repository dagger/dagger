package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// Core names renamed to the GraphQL casing convention at
// engine.IdentifierCasingVersion (see core.CasedNamesView): a field,
// argument or input field is defined once, with its conventional name, and
// installed twice, under that name in the views from the cut on and under
// its old spelling in earlier ones, each within the views it had before. No
// view has both spellings.
//
// A renamed field that had no view filter is view-scoped now: dagql records
// the caller's view in its calls, where it recorded the internal view "", so
// callers of different views (beta.22, beta.23, v1.0.0, and each pre-cut
// view) no longer share its cached or persisted results.

// casedField is field in the views with the conventional core names.
func casedField[T dagql.Typed](field dagql.Field[T]) dagql.Field[T] {
	spec := *field.Spec
	spec.ViewFilter = core.WithinView(spec.ViewFilter, core.CasedNamesView)
	return dagql.Field[T]{Spec: &spec, Func: field.Func}
}

// legacyField is field spelled legacy, in the views before the conventional
// core names.
func legacyField[T dagql.Typed](field dagql.Field[T], legacy string) dagql.Field[T] {
	spec := *field.Spec
	spec.Name = legacy
	spec.ViewFilter = core.WithinView(spec.ViewFilter, core.LegacyNamesView)
	return dagql.Field[T]{Spec: &spec, Func: field.Func}
}

// casedArg is arg in the views with the conventional core names.
func casedArg(arg dagql.Argument) dagql.Argument {
	arg.Spec.ViewFilter = core.WithinView(arg.Spec.ViewFilter, core.CasedNamesView)
	return arg
}

// legacyArg is arg spelled legacy, in the views before the conventional core
// names. The resolver's args struct needs a field for each spelling.
func legacyArg(arg dagql.Argument, legacy string) dagql.Argument {
	arg.Spec.Name = legacy
	arg.Spec.ViewFilter = core.WithinView(arg.Spec.ViewFilter, core.LegacyNamesView)
	return arg
}

// unfilteredFieldLegacyArg is a renamed argument's old spelling legacy, as
// an internal argument of the internal view "", for an argument of a field
// without a view filter. dagql selects every call to such a field in view
// "", whatever its caller's view, so without it the argument a caller before
// the cut passes would be dropped; calls recorded that way still load too.
func unfilteredFieldLegacyArg(legacy string) dagql.Argument {
	return dagql.Arg(legacy).View(core.UnfilteredFieldView).Internal()
}

// accessorField is a getter like the ones dagql reflects from struct fields
// tagged field:"true", for a struct field renamed in the schema, which has
// to be installed by hand under each spelling.
func accessorField[T dagql.Typed, R dagql.Typed](name string, get func(T) R, doc string) dagql.Field[T] {
	field := dagql.Func(name, func(_ context.Context, self T, _ struct{}) (R, error) {
		return get(self), nil
	}).Doc(doc)
	field.Spec.Trivial = true
	switch any(*new(R)).(type) {
	case dagql.String, dagql.Int, dagql.Float, dagql.Boolean:
		field.Spec.DoNotCache = "Scalar getters only read their receiver"
		field.Spec.DeduplicateTelemetry = true
	}
	return field
}
