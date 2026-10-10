package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/naming"
)

type identifierSchema struct{}

var _ SchemaResolvers = &identifierSchema{}

const identifierExperimental = "Identifier casing APIs are likely to change."

// identifierVersion is the first engine version with the identifier API. SDK
// codegen gates on Query.formatIdentifiers being in the schema it generates
// from, so this is also where codegen starts formatting names through the
// engine.
const identifierVersion = "v1.0.0-0"

// formatIdentifiersDoNotCache is why formatIdentifiers and __formatIdentifiers
// skip the cache: they're pure and fast, and caching costs far more than
// formatting. For the core schema's ~960 names, formatting takes about a
// millisecond, but publishing the result to the cache took over 200ms. Nor
// would a cache hit help much: codegen makes one call per name format, each
// with different arguments.
const formatIdentifiersDoNotCache = "Formatting is pure and much cheaper than caching a result with a large list argument."

func (s identifierSchema) Install(srv *dagql.Server) {
	view := AfterVersion(identifierVersion)

	core.IdentifierWordKinds.Install(srv, view)
	core.Casings.Install(srv, view)
	core.AcronymStyles.Install(srv, view)

	srv.InstallObject(dagql.NewClass[*core.Identifier](srv).View(view))
	srv.InstallObject(dagql.NewClass[*core.IdentifierWord](srv).View(view))
	srv.InstallObject(dagql.NewClass[*core.NamingTerm](srv).View(view))

	dagql.Fields[*core.Query]{
		dagql.Func("identifier", s.identifier).
			View(view).
			Experimental(identifierExperimental).
			Doc(`Parse a name in any casing into words.`,
				`Known acronyms and terms come from the naming dictionary; everything
				else falls back to the case heuristic. Errors on non-ASCII input or
				input with no letters or digits.`).
			Args(
				dagql.Arg("name").Doc("The name to parse."),
			),
		dagql.Func("formatIdentifiers", s.formatIdentifiers).
			View(view).
			DoNotCache(formatIdentifiersDoNotCache).
			Experimental(identifierExperimental).
			Doc(`Format many names at once, for codegen. Returns them in input order.`).
			Args(
				dagql.Arg("names").Doc("The names to format, in any casing."),
				dagql.Arg("casing").Doc("The casing to format the names in."),
				dagql.Arg("acronyms").Doc("How to write acronyms and terms where a word starts with a capital."),
			),
		dagql.Func("namingDictionary", s.namingDictionary).
			View(view).
			Experimental(identifierExperimental).
			Doc(`The acronyms and terms used to parse and format identifiers.`),
	}.Install(srv)

	dagql.Fields[*core.Query]{
		// formatIdentifiers for module runtimes, which run codegen for a
		// module's schema but are served at their own engine version, where
		// formatIdentifiers may not exist: installed in every view, hidden
		// from introspection by the __ prefix, and parsing with the dictionary
		// of the given version rather than the caller's. Its casing and
		// acronyms are strings, since the Casing and AcronymStyle enums aren't
		// in older views.
		dagql.Func("__formatIdentifiers", s.formatIdentifiersForVersion).
			DoNotCache(formatIdentifiersDoNotCache).
			Doc(`(Internal-only) Format many names at once, for codegen, with the naming dictionary of an engine version. Returns them in input order.`).
			Args(
				dagql.Arg("names").Doc("The names to format, in any casing."),
				dagql.Arg("casing").Doc("The casing to format the names in: a Casing value."),
				dagql.Arg("acronyms").Doc("How to write acronyms and terms where a word starts with a capital: an AcronymStyle value."),
				dagql.Arg("version").Doc("The engine version whose naming dictionary to parse the names with, e.g. the __schemaVersion of the schema being generated."),
			),
	}.Install(srv)

	dagql.Fields[*core.Identifier]{
		dagql.Func("format", s.format).
			Doc(`Format the identifier in a casing.`).
			Args(
				dagql.Arg("casing").Doc("The casing to format the identifier in."),
				dagql.Arg("acronyms").Doc("How to write acronyms and terms where a word starts with a capital."),
			),
	}.Install(srv)

	dagql.Fields[*core.IdentifierWord]{
		// An explicit nullable accessor: a struct-derived pointer field
		// renders non-null.
		dagql.Func("term", s.wordTerm).
			Doc(`The dictionary entry this word matched, if any.`),
	}.Install(srv)

	dagql.Fields[*core.NamingTerm]{}.Install(srv)
}

// namingDictionaryFor returns the dictionary of the caller's engine version:
// the view recorded in a view-scoped call, else the view of the current
// server (see core.CallerView). Dictionary additions only apply to callers at
// or above the engine version that introduced them.
func namingDictionaryFor(ctx context.Context) *naming.Dictionary {
	return naming.DictionaryFor(string(core.CallerView(ctx)))
}

func (s identifierSchema) identifier(ctx context.Context, _ *core.Query, args struct {
	Name string
}) (*core.Identifier, error) {
	id, err := namingDictionaryFor(ctx).Parse(args.Name)
	if err != nil {
		return nil, err
	}
	return core.NewIdentifier(id), nil
}

type identifierFormatArgs struct {
	Casing   core.Casing
	Acronyms core.AcronymStyle `default:"UPPERCASE"`
}

func (args identifierFormatArgs) naming() (naming.Casing, naming.AcronymStyle, error) {
	casing, err := args.Casing.Naming()
	if err != nil {
		return 0, 0, err
	}
	style, err := args.Acronyms.Naming()
	if err != nil {
		return 0, 0, err
	}
	return casing, style, nil
}

func (s identifierSchema) formatIdentifiers(ctx context.Context, _ *core.Query, args struct {
	Names    []string
	Casing   core.Casing
	Acronyms core.AcronymStyle `default:"UPPERCASE"`
}) ([]dagql.String, error) {
	return formatNames(namingDictionaryFor(ctx), args.Names, args.Casing, args.Acronyms)
}

func (s identifierSchema) formatIdentifiersForVersion(_ context.Context, _ *core.Query, args struct {
	Names    []string
	Casing   string
	Acronyms string `default:"UPPERCASE"`
	Version  string
}) ([]dagql.String, error) {
	return formatNames(naming.DictionaryFor(args.Version), args.Names, core.Casing(args.Casing), core.AcronymStyle(args.Acronyms))
}

// formatNames parses names with dict and formats them in a casing and
// acronym style, in input order.
func formatNames(dict *naming.Dictionary, names []string, casing core.Casing, acronyms core.AcronymStyle) ([]dagql.String, error) {
	namingCasing, style, err := identifierFormatArgs{Casing: casing, Acronyms: acronyms}.naming()
	if err != nil {
		return nil, err
	}
	formatted := make([]dagql.String, len(names))
	for i, name := range names {
		id, err := dict.Parse(name)
		if err != nil {
			return nil, err
		}
		formatted[i] = dagql.String(id.Format(namingCasing, style))
	}
	return formatted, nil
}

func (s identifierSchema) namingDictionary(ctx context.Context, _ *core.Query, _ struct{}) ([]*core.NamingTerm, error) {
	terms := namingDictionaryFor(ctx).Terms()
	out := make([]*core.NamingTerm, len(terms))
	for i, t := range terms {
		out[i] = core.NewNamingTerm(t)
	}
	return out, nil
}

func (s identifierSchema) format(_ context.Context, id *core.Identifier, args identifierFormatArgs) (dagql.String, error) {
	casing, style, err := args.naming()
	if err != nil {
		return "", err
	}
	return dagql.String(id.Naming().Format(casing, style)), nil
}

func (s identifierSchema) wordTerm(_ context.Context, word *core.IdentifierWord, _ struct{}) (dagql.Nullable[*core.NamingTerm], error) {
	if word.Term == nil {
		return dagql.Null[*core.NamingTerm](), nil
	}
	return dagql.NonNull(word.Term), nil
}
