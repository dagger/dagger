package schema

import (
	"context"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/naming"
)

type identifierSchema struct{}

var _ SchemaResolvers = &identifierSchema{}

const identifierExperimental = "Identifier casing APIs are likely to change."

// identifierVersion is the first engine version with the identifier API: its
// types are served on every v1 view, including the views whose modules keep
// the legacy strcase names (before core.IdentifierNamingVersion). Which naming
// rules a module follows is decided by the engine version it declares, capped
// at the engine's own (core.ModuleSource.NamingView), not by the presence of
// this API; Query.naming's version argument selects them.
const identifierVersion = "v1.0.0-0"

// beta25Dictionary is the dictionary v1.0.0-beta.25 parsed every name with:
// K8s and K3s became terms after it.
var beta25Dictionary = naming.MustDictionary(slices.DeleteFunc(naming.Initial.Terms(), func(t naming.Term) bool {
	return t.Spelling == "K8s" || t.Spelling == "K3s"
}))

// formatIdentifiersDoNotCache is why formatIdentifiers skips the cache: it's
// pure and fast, and caching costs far more than formatting. For the core
// schema's ~960 names, formatting takes about a millisecond, but publishing
// the result to the cache took over 200ms. Nor would a cache hit help much:
// codegen makes one call per name format, each with different arguments.
const formatIdentifiersDoNotCache = "Formatting is pure and much cheaper than caching a result with a large list argument."

func (s identifierSchema) Install(srv *dagql.Server) {
	view := AfterVersion(identifierVersion)
	namingView := AfterVersion("v1.0.0-beta.26")
	beta25View := core.WithinView(view, BeforeVersion("v1.0.0-beta.26"))

	core.IdentifierWordKinds.Install(srv, view)
	core.Casings.Install(srv, view)
	core.AcronymStyles.Install(srv, view)

	srv.InstallObject(dagql.NewClass[*core.Identifier](srv).View(view))
	srv.InstallObject(dagql.NewClass[*core.IdentifierWord](srv).View(view))
	srv.InstallObject(dagql.NewClass[*core.NamingTerm](srv).View(view))

	srv.InstallObject(dagql.NewClass[*core.Naming](srv).View(namingView))

	dagql.Fields[*core.Query]{
		dagql.Func("identifier", s.beta25Identifier).
			View(beta25View).
			Experimental(identifierExperimental).
			Doc(`Parse a name in any casing into words.`,
				`Known acronyms and terms come from the naming dictionary; everything
				else falls back to the case heuristic. Errors on non-ASCII input,
				input with no letters or digits, or input over 256 bytes.`).
			Args(
				dagql.Arg("name").Doc("The name to parse."),
			),
		dagql.Func("formatIdentifiers", s.beta25FormatIdentifiers).
			View(beta25View).
			DoNotCache(formatIdentifiersDoNotCache).
			Experimental(identifierExperimental).
			Doc(`Format many names at once, for codegen. Returns them in input order.`).
			Args(
				dagql.Arg("names").Doc("The names to format, in any casing."),
				dagql.Arg("casing").Doc("The casing to format the names in."),
				dagql.Arg("acronyms").Doc("How to write acronyms and terms where a word starts with a capital."),
				dagql.Arg("version").Doc(
					`The engine version whose naming dictionary to parse the names with,
					e.g. the __schemaVersion of a schema being generated. Defaults to the
					caller's engine version.`),
			),
		dagql.Func("namingDictionary", s.beta25NamingDictionary).
			View(beta25View).
			Experimental(identifierExperimental).
			Doc(`The acronyms and terms used to parse and format identifiers.`),
		dagql.Func("naming", s.naming).
			View(namingView).
			Experimental(identifierExperimental).
			Doc(`The engine's identifier naming rules at an engine version.`).
			Args(
				dagql.Arg("version").Doc(
					`The engine version to answer for, e.g. the engine version a module
					declares. Defaults to the caller's. Normalized as a module's is: a
					version past the engine's is capped at the engine's own.`),
			),
	}.Install(srv)

	dagql.Fields[*core.Naming]{
		dagql.Func("version", s.namingVersion).
			Doc(`The normalized engine version these rules answer for.`),
		dagql.Func("identifier", s.identifier).
			Doc(`Parse a name in any casing into words.`,
				`Known acronyms and terms come from the naming dictionary; everything
				else falls back to the case heuristic. Errors on non-ASCII input,
				input with no letters or digits, or input over 256 bytes.`).
			Args(
				dagql.Arg("name").Doc("The name to parse."),
			),
		dagql.Func("formatIdentifiers", s.formatIdentifiers).
			DoNotCache(formatIdentifiersDoNotCache).
			Doc(`Format many names at once, for codegen. Returns them in input order.`).
			Args(
				dagql.Arg("names").Doc("The names to format, in any casing."),
				dagql.Arg("casing").Doc("The casing to format the names in."),
				dagql.Arg("acronyms").Doc("How to write acronyms and terms where a word starts with a capital."),
			),
		dagql.Func("dictionary", s.dictionary).
			Doc(`The acronyms and terms used to parse and format identifiers.`),
		dagql.Func("module", s.module).
			Doc(`The names the engine gives the declarations of a module named by these rules.`).
			Args(
				dagql.Arg("name").Doc(`The module's name, which namespaces its types.`),
				dagql.Arg("originalName").Doc(
					`The module's original name, when it's loaded under another (as a
					dependency alias): its main object and namespaced type names are
					spelled after it. Defaults to name.`),
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

// naming returns the naming of the requested engine version, else of the
// caller's: the view recorded in the call, which is view-scoped (see
// core.CallerView).
func (s identifierSchema) naming(ctx context.Context, _ *core.Query, args struct {
	Version dagql.Optional[dagql.String]
}) (*core.Naming, error) {
	version := string(core.CallerView(ctx))
	if args.Version.Valid && args.Version.Value != "" {
		version = string(args.Version.Value)
	}
	return core.NamingFor(version), nil
}

func (s identifierSchema) beta25Identifier(_ context.Context, _ *core.Query, args struct{ Name string }) (*core.Identifier, error) {
	return parseIdentifier(beta25Dictionary, args.Name)
}

func (s identifierSchema) beta25FormatIdentifiers(_ context.Context, _ *core.Query, args struct {
	Names    []string
	Casing   core.Casing
	Acronyms core.AcronymStyle `default:"UPPERCASE"`
	// Unread: v1.0.0-beta.25 parsed names of every version with one dictionary.
	Version dagql.Optional[dagql.String]
}) ([]dagql.String, error) {
	return formatNames(beta25Dictionary, args.Names, args.Casing, args.Acronyms)
}

func (s identifierSchema) beta25NamingDictionary(context.Context, *core.Query, struct{}) ([]*core.NamingTerm, error) {
	return namingTerms(beta25Dictionary), nil
}

func (s identifierSchema) namingVersion(_ context.Context, n *core.Naming, _ struct{}) (dagql.String, error) {
	return dagql.String(n.Version), nil
}

func (s identifierSchema) identifier(_ context.Context, n *core.Naming, args struct {
	Name string
}) (*core.Identifier, error) {
	return parseIdentifier(n.Dictionary(), args.Name)
}

func parseIdentifier(dict *naming.Dictionary, name string) (*core.Identifier, error) {
	id, err := dict.Parse(name)
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

func (s identifierSchema) formatIdentifiers(_ context.Context, n *core.Naming, args struct {
	Names    []string
	Casing   core.Casing
	Acronyms core.AcronymStyle `default:"UPPERCASE"`
}) ([]dagql.String, error) {
	return formatNames(n.Dictionary(), args.Names, args.Casing, args.Acronyms)
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

func (s identifierSchema) module(_ context.Context, n *core.Naming, args struct {
	Name         string
	OriginalName dagql.Optional[dagql.String]
}) (*core.ModuleNaming, error) {
	originalName := args.Name
	if args.OriginalName.Valid {
		originalName = string(args.OriginalName.Value)
	}
	return n.Module(args.Name, originalName), nil
}

func (s identifierSchema) dictionary(_ context.Context, n *core.Naming, _ struct{}) ([]*core.NamingTerm, error) {
	return namingTerms(n.Dictionary()), nil
}

func namingTerms(dict *naming.Dictionary) []*core.NamingTerm {
	terms := dict.Terms()
	out := make([]*core.NamingTerm, len(terms))
	for i, t := range terms {
		out[i] = core.NewNamingTerm(t)
	}
	return out
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
