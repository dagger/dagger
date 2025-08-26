package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"runtime/debug"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
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

func (funcs goTemplateFuncs) TypeDefs() (string, error) {
	dag := funcs.cfg.Dag
	module := dag.Module()

	err := funcs.visitTypes(
		false,
		&visitorFuncs{
			RootVisitor: func(pkgDoc string) error {
				if pkgDoc != "" {
					module = module.WithDescription(pkgDoc)
				}
				return nil
			},
			StructVisitor: func(ps *parseState, named *types.Named, obj *types.TypeName, objTypeSpec *parsedObjectType, strct *types.Struct) error {
				var err error
				typeDef, err := objTypeSpec.TypeDefObject(dag)
				if err != nil {
					return err
				}
				module = module.WithObject(typeDef)
				return nil
			},
			IfaceVisitor: func(ps *parseState, named *types.Named, obj *types.TypeName, ifaceTypeSpec *parsedIfaceType, iface *types.Interface) error {
				var err error
				typeDef, err := ifaceTypeSpec.TypeDefObject(dag)
				if err != nil {
					return err
				}
				module = module.WithInterface(typeDef)
				return nil
			},
			EnumVisitor: func(ps *parseState, named *types.Named, obj *types.TypeName, enumTypeSpec *parsedEnumType, enum *types.Basic) error {
				var err error
				typeDef, err := enumTypeSpec.TypeDefObject(dag)
				if err != nil {
					return err
				}
				module = module.WithEnum(typeDef)
				return nil
			},
		},
	)
	if err != nil {
		if errors.Is(err, errEmptyCode) {
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

	id, err := module.ID(funcs.ctx)
	if err != nil {
		return "", err
	}
	jsonID, err := json.Marshal(id)
	if err != nil {
		return "", err
	}
	return string(jsonID), nil
}
