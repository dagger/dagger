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

func (s identifierSchema) Install(srv *dagql.Server) {
	view := AfterVersion("v1.0.0-0")

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

// namingDictionaryFor returns the dictionary for the caller. There is one
// dictionary so far; additions will be selected by engine version.
func namingDictionaryFor(context.Context) *naming.Dictionary {
	return naming.Latest
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
	casing, style, err := identifierFormatArgs{Casing: args.Casing, Acronyms: args.Acronyms}.naming()
	if err != nil {
		return nil, err
	}
	dict := namingDictionaryFor(ctx)
	formatted := make([]dagql.String, len(args.Names))
	for i, name := range args.Names {
		id, err := dict.Parse(name)
		if err != nil {
			return nil, err
		}
		formatted[i] = dagql.String(id.Format(casing, style))
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
