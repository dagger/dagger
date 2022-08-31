package main

import (
	_ "embed"
	"fmt"
	"go/types"
	"os"
	"path/filepath"

	"github.com/99designs/gqlgen/api"
	"github.com/99designs/gqlgen/codegen"
	gqlconfig "github.com/99designs/gqlgen/codegen/config"
	"github.com/99designs/gqlgen/codegen/templates"
	"github.com/Khan/genqlient/generate"
	"github.com/dagger/cloak/core"
	"github.com/vektah/gqlparser/v2/ast"
)

//go:embed templates/go.main.gotpl
var mainTmpl string

//go:embed templates/go.generated.gotpl
var generatedTmpl string

func generateGoImplStub(ext, coreExt *core.Extension) error {
	cfg := gqlconfig.DefaultConfig()
	cfg.SkipModTidy = true
	cfg.Exec = gqlconfig.ExecConfig{Filename: filepath.Join(workdir, filepath.Dir(configPath), "_deleteme.go"), Package: "main"}
	cfg.SchemaFilename = nil
	cfg.Sources = []*ast.Source{{Input: ext.Schema}}
	cfg.Model = gqlconfig.PackageConfig{
		Filename: filepath.Join(generateOutputDir, "models.go"),
		Package:  "main",
	}
	cfg.Models = gqlconfig.TypeMap{
		"SecretID": gqlconfig.TypeMapEntry{
			Model: gqlconfig.StringList{"github.com/dagger/cloak/sdk/go/dagger.SecretID"},
		},
		"FSID": gqlconfig.TypeMapEntry{
			Model: gqlconfig.StringList{"github.com/dagger/cloak/sdk/go/dagger.FSID"},
		},
		"Filesystem": gqlconfig.TypeMapEntry{
			Model: gqlconfig.StringList{"github.com/dagger/cloak/sdk/go/dagger.Filesystem"},
			Fields: map[string]gqlconfig.TypeMapField{
				"exec":        {Resolver: false},
				"dockerbuild": {Resolver: false},
				"file":        {Resolver: false},
			},
		},
		"Exec": gqlconfig.TypeMapEntry{
			Model: gqlconfig.StringList{"github.com/dagger/cloak/sdk/go/dagger.Exec"},
			Fields: map[string]gqlconfig.TypeMapField{
				"fs":       {Resolver: false},
				"stdout":   {Resolver: false},
				"stderr":   {Resolver: false},
				"exitcode": {Resolver: false},
				"mount":    {Resolver: false},
			},
		},
	}

	if err := gqlconfig.CompleteConfig(cfg); err != nil {
		return fmt.Errorf("error completing config: %w", err)
	}
	defer os.Remove(cfg.Exec.Filename)
	if err := api.Generate(cfg, api.AddPlugin(plugin{
		mainPath:      filepath.Join(generateOutputDir, "main.go"),
		generatedPath: filepath.Join(generateOutputDir, "generated.go"),
		coreSchema:    coreExt.Schema,
	})); err != nil {
		return fmt.Errorf("error generating code: %w", err)
	}
	return nil
}

func generateGoClientStubs(subdir string) error {
	cfg := &generate.Config{
		Schema:     generate.StringList{"schema.graphql"},
		Operations: generate.StringList{"operations.graphql"},
		Generated:  "generated.go",
		Bindings: map[string]*generate.TypeBinding{
			"Filesystem": {Type: "github.com/dagger/cloak/sdk/go/dagger.Filesystem"},
			"Exec":       {Type: "github.com/dagger/cloak/sdk/go/dagger.Exec"},
			"FSID":       {Type: "github.com/dagger/cloak/sdk/go/dagger.FSID"},
			"SecretID":   {Type: "github.com/dagger/cloak/sdk/go/dagger.SecretID"},
		},
		ClientGetter: "github.com/dagger/cloak/sdk/go/dagger.Client",
	}
	if err := cfg.ValidateAndFillDefaults(subdir); err != nil {
		return err
	}
	generated, err := generate.Generate(cfg)
	if err != nil {
		return err
	}
	for filename, content := range generated {
		if err := os.WriteFile(filename, content, 0600); err != nil {
			return err
		}
	}
	return nil
}

type plugin struct {
	mainPath      string
	generatedPath string
	coreSchema    string
}

func (plugin) Name() string {
	return "cloakgen"
}

func (p plugin) InjectSourceEarly() *ast.Source {
	return &ast.Source{BuiltIn: true, Input: p.coreSchema}
}

func (p plugin) GenerateCode(data *codegen.Data) error {
	file := File{}

	typesByName := make(map[string]types.Type)
	for _, o := range data.Objects {
		if o.Name == "Query" {
			// only include fields under query from the current schema, not any external imported ones like `core`
			var queryFields []*codegen.Field
			for _, f := range o.Fields {
				if !f.TypeReference.Definition.BuiltIn {
					queryFields = append(queryFields, f)
				}
			}
			o.Fields = queryFields
		} else if o.BuiltIn || o.IsReserved() {
			continue
		}
		var hasResolvers bool
		for _, f := range o.Fields {
			if !f.IsReserved() {
				hasResolvers = true
			}
		}
		if !hasResolvers {
			continue
		}
		file.Objects = append(file.Objects, o)
		typesByName[o.Reference().String()] = o.Reference()
		for _, f := range o.Fields {
			f.MethodHasContext = true
			resolver := Resolver{o, f, "", ""}
			file.Resolvers = append(file.Resolvers, &resolver)
			typesByName[f.TypeReference.GO.String()] = f.TypeReference.GO
			for _, arg := range f.Args {
				typesByName[arg.TypeReference.GO.String()] = arg.TypeReference.GO
			}
		}
	}

	resolverBuild := &ResolverBuild{
		File:        &file,
		PackageName: "main",
		HasRoot:     true,
		typesByName: typesByName,
	}

	if err := templates.Render(templates.Options{
		PackageName: "main",
		Filename:    p.mainPath,
		Data:        resolverBuild,
		Packages:    data.Config.Packages,
		Template:    mainTmpl,
	}); err != nil {
		return err
	}

	if err := templates.Render(templates.Options{
		PackageName:     "main",
		Filename:        p.generatedPath,
		Data:            resolverBuild,
		Packages:        data.Config.Packages,
		Template:        generatedTmpl,
		GeneratedHeader: true,
	}); err != nil {
		return err
	}

	return nil
}

type ResolverBuild struct {
	*File
	HasRoot      bool
	PackageName  string
	ResolverType string
	typesByName  map[string]types.Type
}

func (r ResolverBuild) ShortTypeName(name string) string {
	shortName := templates.CurrentImports.LookupType(r.typesByName[name])
	if shortName == "*<nil>" || shortName == "<nil>" {
		return ""
	}
	return shortName
}

func (r ResolverBuild) PointedToShortTypeName(name string) string {
	t, ok := r.typesByName[name].(*types.Pointer)
	if !ok {
		return ""
	}
	shortName := templates.CurrentImports.LookupType(t.Elem())
	if shortName == "*<nil>" || shortName == "<nil>" {
		return ""
	}
	return shortName
}

type File struct {
	// These are separated because the type definition of the resolver object may live in a different file from the
	// resolver method implementations, for example when extending a type in a different graphql schema file
	Objects         []*codegen.Object
	Resolvers       []*Resolver
	RemainingSource string
}

type Resolver struct {
	Object         *codegen.Object
	Field          *codegen.Field
	Comment        string
	Implementation string
}

func (r *Resolver) HasArgs() bool {
	return len(r.Field.Args) > 0
}

func (r *Resolver) IncludeParentObject() bool {
	return !r.HasArgs() && !r.Object.Root
}

func (r *Resolver) MethodSignature() string {
	if r.Object.Kind == ast.InputObject {
		return fmt.Sprintf("(ctx context.Context, obj %s, data %s) error",
			templates.CurrentImports.LookupType(r.Object.Reference()),
			templates.CurrentImports.LookupType(r.Field.TypeReference.GO),
		)
	}

	res := "(ctx context.Context"

	if r.IncludeParentObject() {
		res += fmt.Sprintf(", obj %s", templates.CurrentImports.LookupType(r.Object.Reference()))
	}
	for _, arg := range r.Field.Args {
		res += fmt.Sprintf(", %s %s", arg.VarName, templates.CurrentImports.LookupType(arg.TypeReference.GO))
	}

	result := templates.CurrentImports.LookupType(r.Field.TypeReference.GO)
	res += fmt.Sprintf(") (%s, error)", result)
	return res
}
