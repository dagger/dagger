package templates

import (
	"fmt"
	"go/types"
	"os"
	"runtime/debug"
	"sort"
)

type (
	rootVisitor   func(description string) error
	structVisitor func(*parseState, *types.Named, *types.TypeName, *parsedObjectType, *types.Struct) error
	ifaceVisitor  func(*parseState, *types.Named, *types.TypeName, *parsedIfaceType, *types.Interface) error
	enumVisitor   func(*parseState, *types.Named, *types.TypeName, *parsedEnumType, *types.Basic) error

	visitorFuncs struct {
		RootVisitor   rootVisitor
		StructVisitor structVisitor
		IfaceVisitor  ifaceVisitor
		EnumVisitor   enumVisitor
	}
)

func (v *visitorFuncs) isValid() bool {
	return v.RootVisitor != nil &&
		v.StructVisitor != nil &&
		v.IfaceVisitor != nil &&
		v.EnumVisitor != nil
}

var (
	errEmptyCode = fmt.Errorf("no code yet")
)

func (funcs goTemplateFuncs) visitTypes(
	strict bool,
	visitorFuncs *visitorFuncs,
) error {
	if !visitorFuncs.isValid() {
		return fmt.Errorf("visitorFuncs is invalid, define all functions. This is a bug")
	}

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
		return errEmptyCode
	}

	ps := &parseState{
		pkg:        funcs.modulePkg,
		fset:       funcs.moduleFset,
		schema:     funcs.schema,
		moduleName: funcs.cfg.ModuleConfig.ModuleName,

		methods: make(map[string][]method),
	}

	tps, err := funcs.getTypes(ps, strict)
	if err != nil {
		return err
	}

	if err = visitorFuncs.RootVisitor(ps.pkgDoc()); err != nil {
		return err
	}

	added := map[string]struct{}{}

	for len(tps) != 0 {
		// Sort types by source position to ensure deterministic ordering while
		// preserving declaration order. This is especially important for:
		// - MarshalJSON/UnmarshalJSON methods
		// - Sub-types from struct fields (which should appear in field order)
		sort.Slice(tps, func(i, j int) bool {
			iNamed, iOk := tps[i].(*types.Named)
			jNamed, jOk := tps[j].(*types.Named)
			if iOk && jOk {
				// Sort by source position (declaration order)
				return iNamed.Obj().Pos() < jNamed.Obj().Pos()
			}
			// If either is not named, fallback to string representation
			return tps[i].String() < tps[j].String()
		})

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

				if err = visitorFuncs.StructVisitor(ps, named, obj, objTypeSpec, strct); err != nil {
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

				if err = visitorFuncs.IfaceVisitor(ps, named, obj, ifaceTypeSpec, iface); err != nil {
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

				if err = visitorFuncs.EnumVisitor(ps, named, obj, enumTypeSpec, enum); err != nil {
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
