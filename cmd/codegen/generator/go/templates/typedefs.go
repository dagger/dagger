package templates

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"runtime/debug"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"golang.org/x/tools/go/packages"
)

type TypeDefsGenerator interface {
	TypeDefs() (string, error)
}

func GoTypeDefsGenerator(
	ctx context.Context,
	schema *introspection.Schema,
	schemaVersion string,
	cfg generator.Config,
	pkg *packages.Package,
	fset *token.FileSet,
	pass int,
) TypeDefsGenerator {
	return goTemplateFuncs{
		CommonFunctions: generator.NewCommonFunctions(schemaVersion, &FormatTypeFunc{}),
		ctx:             ctx,
		cfg:             cfg,
		modulePkg:       pkg,
		moduleFset:      fset,
		schema:          schema,
		schemaVersion:   schemaVersion,
		pass:            pass,
	}
}

type (
	rootVisitor   func(description string) error
	structVisitor func(*parseState, *types.Named, *types.TypeName, *parsedObjectType, *types.Struct) error
	ifaceVisitor  func(*parseState, *types.Named, *types.TypeName, *parsedIfaceType, *types.Interface) error
	enumVisitor   func(*parseState, *types.Named, *types.TypeName, *parsedEnumType, *types.Basic) error
)

var (
	emptyCodeError = fmt.Errorf("no code yet")
)

func (funcs goTemplateFuncs) visitTypes(
	strict bool,
	rootVisitor rootVisitor,
	structVisitor structVisitor,
	ifaceVisitor ifaceVisitor,
	enumVisitor enumVisitor,
) error {
	// HACK: the code in this func can be pretty flaky and tricky to debug -
	// it's much easier to debug when we actually have stack traces, so we grab
	// those on a panic
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintf(os.Stderr, "internal error during module code generation: %v\n", r)
			debug.PrintStack()
			panic(r)
		}
	}()

	if funcs.modulePkg == nil {
		return emptyCodeError
	}

	ps := &parseState{
		pkg:        funcs.modulePkg,
		fset:       funcs.moduleFset,
		schema:     funcs.schema,
		moduleName: funcs.cfg.TypeDefGeneratorConfig.ModuleName,

		methods: make(map[string][]method),
	}

	tps, err := funcs.getTypes(ps, strict)
	if err != nil {
		return err
	}

	if err = rootVisitor(ps.pkgDoc()); err != nil {
		return err
	}

	added := map[string]struct{}{}

	for len(tps) != 0 {
		var nextTps []types.Type
		for _, tp := range tps {
			tp = dealias(tp)

			named, isNamed := tp.(*types.Named)
			if !isNamed {
				continue
			}

			obj := named.Obj()
			basePkg := funcs.modulePkg.Types.Path()
			if obj.Pkg().Path() != basePkg && !ps.isDaggerGenerated(obj) {
				// the type must be created in the target package (if not a
				// generated type)
				return fmt.Errorf("cannot code-generate for foreign type %s", obj.Name())
			}
			if !obj.Exported() {
				// the type must be exported
				return fmt.Errorf("cannot code-generate unexported type %s", obj.Name())
			}

			// avoid adding a struct definition twice (if it's referenced in two function signatures)
			if _, ok := added[obj.Pkg().Path()+"/"+obj.Name()]; ok {
				continue
			}

			switch underlyingObj := named.Underlying().(type) {
			case *types.Struct:
				strct := underlyingObj
				objTypeSpec, err := ps.parseGoStruct(strct, named)
				if err != nil {
					return err
				}
				if objTypeSpec == nil {
					// not including in module schema, skip it
					continue
				}

				if err = structVisitor(ps, named, obj, objTypeSpec, strct); err != nil {
					return err
				}

				added[obj.Pkg().Path()+"/"+obj.Name()] = struct{}{}

				// If the object has any extra sub-types (e.g. for function return
				// values), add them to the list of types to process
				nextTps = append(nextTps, objTypeSpec.GoSubTypes()...)
			case *types.Interface:
				iface := underlyingObj
				ifaceTypeSpec, err := ps.parseGoIface(iface, named)
				if err != nil {
					return err
				}
				if ifaceTypeSpec == nil {
					// not including in module schema, skip it
					continue
				}

				if err = ifaceVisitor(ps, named, obj, ifaceTypeSpec, iface); err != nil {
					return err
				}

				added[obj.Pkg().Path()+"/"+obj.Name()] = struct{}{}

				// If the object has any extra sub-types (e.g. for function return
				// values), add them to the list of types to process
				nextTps = append(nextTps, ifaceTypeSpec.GoSubTypes()...)
			case *types.Basic:
				if ps.isDaggerGenerated(obj) {
					continue
				}
				enum := underlyingObj
				enumTypeSpec, err := ps.parseGoEnum(enum, named)
				if err != nil {
					return err
				}
				if enumTypeSpec == nil {
					// not including in module schema, skip it
					continue
				}

				if err = enumVisitor(ps, named, obj, enumTypeSpec, enum); err != nil {
					return err
				}

				added[obj.Pkg().Path()+"/"+obj.Name()] = struct{}{}

				// If the object has any extra sub-types (e.g. for function return
				// values), add them to the list of types to process
				nextTps = append(nextTps, enumTypeSpec.GoSubTypes()...)
			}
		}

		tps, nextTps = nextTps, nil
	}

	return nil
}

func (funcs goTemplateFuncs) TypeDefs() (string, error) {
	module := &core.Module{}

	err := funcs.visitTypes(
		false,
		func(pkgDoc string) error {
			if pkgDoc != "" {
				module = module.WithDescription(pkgDoc)
			}
			return nil
		},
		func(ps *parseState, named *types.Named, obj *types.TypeName, objTypeSpec *parsedObjectType, strct *types.Struct) error {
			var err error
			typeDef, err := objTypeSpec.TypeDefObject()
			if err != nil {
				return err
			}
			module, err = module.WithObject(funcs.ctx, typeDef)
			return err
		},
		func(ps *parseState, named *types.Named, obj *types.TypeName, ifaceTypeSpec *parsedIfaceType, iface *types.Interface) error {
			var err error
			typeDef, err := ifaceTypeSpec.TypeDefObject()
			if err != nil {
				return err
			}
			module, err = module.WithInterface(funcs.ctx, typeDef)
			return err
		},
		func(ps *parseState, named *types.Named, obj *types.TypeName, enumTypeSpec *parsedEnumType, enum *types.Basic) error {
			var err error
			typeDef, err := enumTypeSpec.TypeDefObject()
			if err != nil {
				return err
			}
			module, err = module.WithEnum(funcs.ctx, typeDef)
			return err
		})
	if err != nil {
		if errors.Is(err, emptyCodeError) {
			return "", fmt.Errorf("no code yet")
		}
		return "", err
	}

	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintf(os.Stderr, "internal error during module type defs generation: %v\n", r)
			debug.PrintStack()
			panic(r)
		}
	}()

	return module.ToJSONString()
}
