package dagger

import (
	"context"
	"flag"
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"runtime"
	"strings"

	"github.com/iancoleman/strcase"
)

func (r *Environment) WithArtifact_(in any) *Environment {
	flag.Parse()

	// TODO: dedupe huge chunks of code
	typ := reflect.TypeOf(in)
	if typ.Kind() != reflect.Func {
		writeErrorf(fmt.Errorf("expected func, got %v", typ))
	}
	val := reflect.ValueOf(in)
	name := runtime.FuncForPC(val.Pointer()).Name()
	if name == "" {
		writeErrorf(fmt.Errorf("anonymous functions are not supported"))
	}
	fn := &goFunc{
		name: name,
		typ:  typ,
		val:  val,
	}
	for i := 0; i < fn.typ.NumIn(); i++ {
		inputParam := fn.typ.In(i)
		fn.args = append(fn.args, &goParam{
			typ: inputParam,
		})
	}
	for i := 0; i < fn.typ.NumOut(); i++ {
		outputParam := fn.typ.Out(i)
		fn.returns = append(fn.returns, &goParam{
			typ: outputParam,
		})
	}
	if len(fn.returns) > 2 {
		writeErrorf(fmt.Errorf("expected 1 or 2 return values, got %d", len(fn.returns)))
	}

	filePath, lineNum := fn.srcPathAndLine()
	// TODO: cache parsed files
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, filePath, nil, parser.ParseComments)
	if err != nil {
		writeErrorf(fmt.Errorf("parse file: %w", err))
	}
	goast.Inspect(parsed, func(n goast.Node) bool {
		if n == nil {
			return false
		}
		switch decl := n.(type) {
		case *goast.FuncDecl:
			astStart := fileSet.PositionFor(decl.Pos(), false)
			astEnd := fileSet.PositionFor(decl.End(), false)
			// lineNum can be inside the function body due to optimizations that set it to
			// the location of the return statement
			if lineNum < astStart.Line || lineNum > astEnd.Line {
				return true
			}

			fn.name = decl.Name.Name
			fn.doc = strings.TrimSpace(decl.Doc.Text())

			fnArgs := fn.args
			if decl.Recv != nil {
				// ignore the object receiver for now
				fnArgs = fnArgs[1:]
				fn.hasReceiver = true
			}
			astParamList := decl.Type.Params.List
			argIndex := 0
			for _, param := range astParamList {
				// if the signature is like func(a, b string), then a and b are in the same Names slice
				for _, name := range param.Names {
					fnArgs[argIndex].name = name.Name
					argIndex++
				}
			}
			return false

		case *goast.GenDecl:
		default:
		}
		return true
	})

	artifact := defaultContext.Client().EnvironmentArtifact()
	artifact = artifact.
		WithName(strcase.ToLowerCamel(fn.name)).
		WithDescription(fn.doc)

	for i, param := range fn.args {
		// skip receiver
		if fn.hasReceiver && i == 0 {
			continue
		}

		// skip Context
		if param.typ == daggerContextT {
			continue
		}
		artifact = artifact.WithFlag(param.name, EnvironmentArtifactWithFlagOpts{
			Description: "TODO",
		})
	}

	fn.resultConverter = func(ctx context.Context, result any) (any, error) {
		containerType := reflect.TypeOf((*Container)(nil))
		directoryType := reflect.TypeOf((*Directory)(nil))
		fileType := reflect.TypeOf((*File)(nil))
		returntype := fn.returns[0].typ
		switch {
		case returntype == containerType:
			artifact = artifact.WithContainer(result.(*Container))
		case returntype == directoryType:
			artifact = artifact.WithDirectory(result.(*Directory))
		case returntype == fileType:
			artifact = artifact.WithFile(result.(*File))
		default:
			return nil, fmt.Errorf("unsupported return type %v", returntype)
		}
		return artifact.ID(ctx)
	}

	resolvers[lowerCamelCase(fn.name)] = fn
	if !getSchema {
		return r
	}
	return r.WithArtifact(artifact)
}
