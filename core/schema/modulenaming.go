package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// moduleNamingDoNotCache is why ModuleNaming's fields skip the cache, like
// formatIdentifiers: they're pure and fast, and caching a result keyed by a
// large list argument costs far more than computing it.
const moduleNamingDoNotCache = "Naming is pure and much cheaper than caching a result with a large list argument."

// installModuleNaming installs ModuleSource.naming and ModuleNaming. Like the
// identifier API, they're served on every v1 view (identifierVersion): SDK
// modules that call them declare pre-cut versions, and the answer depends on
// the version the module is named by, not the caller's view.
func (s *moduleSourceSchema) installModuleNaming(dag *dagql.Server) {
	view := AfterVersion(identifierVersion)

	dag.InstallObject(dagql.NewClass[*core.ModuleNaming](dag).View(view))

	dagql.Fields[*core.ModuleSource]{
		dagql.Func("naming", s.moduleSourceNaming).
			View(view).
			Experimental(identifierExperimental).
			Doc(`The names the engine gives this module's own declarations, by the naming rules of the version the module is named by: its engine version, capped at the engine's own.`,
				`Modules declaring a version before the identifier casing cut get the legacy strcase names; later ones get the engine's identifier naming rules.`,
				`For SDK codegen, which must reproduce these names before the module is loaded: this reads only the source's name, original name and engine version, so it answers before the module's entrypoint is generated.`,
				`It answers for the module under its own name (moduleName). A workspace or toolchain that loads the module under another name namespaces its types under that name instead, which this doesn't reflect.`),
	}.Install(dag)

	dagql.Fields[*core.ModuleNaming]{
		dagql.Func("typeNames", s.moduleNamingTypeNames).
			DoNotCache(moduleNamingDoNotCache).
			Doc(`The schema names of objects, interfaces and enums this module declares, as the engine installs them: namespaced under the module's name, in input order.`,
				`A name is prefixed with the module's name unless it already starts with the module's name on a word boundary. With the engine's identifier naming rules: HttpClient in module my-mod is MyModHTTPClient; Postman in module post is PostPostman, while PostMan stays PostMan; K8STools in module k8s-tools is K8sTools, the module's own name. With the legacy strcase rules, HttpClient in module my-mod is MyModHttpClient.`,
				`Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.`).
			Args(
				dagql.Arg("names").Doc(`The type names as the module's source declares them.`),
			),
		dagql.Func("fieldNames", s.moduleNamingFieldNames).
			DoNotCache(moduleNamingDoNotCache).
			Doc(`The schema names of functions, object fields and function arguments this module declares, including interface functions, in input order.`,
				`With the engine's identifier naming rules, acronyms are written like words: getURL is getUrl, baseURL is baseUrl, HTTPClient is httpClient. With the legacy strcase rules, HTTPClient is httpclient and e2eTest is e2ETest.`,
				`Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.`).
			Args(
				dagql.Arg("names").Doc(`The function, field and argument names as the module's source declares them.`),
			),
		dagql.Func("enumMemberNames", s.moduleNamingEnumMemberNames).
			DoNotCache(moduleNamingDoNotCache).
			Doc(`The schema names of enum members this module declares, in input order: SCREAMING_SNAKE_CASE.`,
				`A name already written in GraphQL's enum member convention (HTTP2, P_256) is kept as it is. With the engine's identifier naming rules, Arm64 is ARM64 and GitHub is GITHUB; with the legacy strcase rules, Arm64 is ARM_64.`,
				`Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.`).
			Args(
				dagql.Arg("names").Doc(`The enum member names as the module's source declares them.`),
			),
		dagql.Func("mainObject", s.moduleNamingMainObject).
			DoNotCache(moduleNamingDoNotCache).
			Doc(`Which of the candidates, the names of objects this module declares, the engine takes as the module's main object; null if none.`,
				`The engine finds the main object by the module's name, in any casing: the first candidate, in input order, whose name normalizes to the same type name as the module's (K8STools for module k8s-tools, with the engine's identifier naming rules). Failing that, with the engine's identifier naming rules only, the only candidate spelled the same once lowercased without word boundaries (Sha256Sum for module sha256sum); several such candidates are ambiguous, and give null.`,
				`Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.`).
			Args(
				dagql.Arg("candidates").Doc(`The object names as the module's source declares them.`),
			),
	}.Install(dag)
}

func (s *moduleSourceSchema) moduleSourceNaming(_ context.Context, src *core.ModuleSource, _ struct{}) (*core.ModuleNaming, error) {
	return src.Naming(), nil
}

type moduleNamingNamesArgs struct {
	Names []string
}

func moduleNamingStrings(names []string, err error) ([]dagql.String, error) {
	if err != nil {
		return nil, err
	}
	out := make([]dagql.String, len(names))
	for i, name := range names {
		out[i] = dagql.String(name)
	}
	return out, nil
}

func (s *moduleSourceSchema) moduleNamingTypeNames(_ context.Context, mn *core.ModuleNaming, args moduleNamingNamesArgs) ([]dagql.String, error) {
	return moduleNamingStrings(mn.TypeNames(args.Names))
}

func (s *moduleSourceSchema) moduleNamingFieldNames(_ context.Context, mn *core.ModuleNaming, args moduleNamingNamesArgs) ([]dagql.String, error) {
	return moduleNamingStrings(mn.FieldNames(args.Names))
}

func (s *moduleSourceSchema) moduleNamingEnumMemberNames(_ context.Context, mn *core.ModuleNaming, args moduleNamingNamesArgs) ([]dagql.String, error) {
	return moduleNamingStrings(mn.EnumMemberNames(args.Names))
}

func (s *moduleSourceSchema) moduleNamingMainObject(_ context.Context, mn *core.ModuleNaming, args struct {
	Candidates []string
}) (dagql.Nullable[dagql.String], error) {
	name, ok, err := mn.MainObject(args.Candidates)
	if err != nil {
		return dagql.Null[dagql.String](), err
	}
	if !ok {
		return dagql.Null[dagql.String](), nil
	}
	return dagql.NonNull(dagql.String(name)), nil
}
