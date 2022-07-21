package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	tools "github.com/bhoriuchi/graphql-go-tools"
	"github.com/containerd/containerd/platforms"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/printer"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	daggerSockName = "dagger-sock"
)

// FS is either llb representing the filesystem or a graphql query for obtaining that llb
// This is opaque to clients; to them FS is a scalar.
type FS struct {
	PB    *pb.Definition
	Query string
	Vars  map[string]interface{} // NOTE: encoding/json sorts by key so this *should* be deterministic (?)
}

// FS encodes to the base64 encoding of its JSON representation
func (fs FS) MarshalText() ([]byte, error) {
	type marshalFS FS
	jsonBytes, err := json.Marshal(marshalFS(fs))
	if err != nil {
		return nil, err
	}
	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)
	return b64Bytes, nil
}

func (fs *FS) UnmarshalText(b64Bytes []byte) error {
	type marshalFS FS
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(b64Bytes)))
	n, err := base64.StdEncoding.Decode(jsonBytes, b64Bytes)
	if err != nil {
		return fmt.Errorf("failed to unmarshal fs bytes: %v", err)
	}
	jsonBytes = jsonBytes[:n]
	var result marshalFS
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return fmt.Errorf("failed to unmarshal result: %v", err)
	}
	fs.PB = result.PB
	fs.Query = result.Query
	fs.Vars = result.Vars
	return nil
}

func (fs FS) ToState() (llb.State, error) {
	if fs.PB == nil {
		return llb.State{}, fmt.Errorf("FS is not evaluated")
	}
	defop, err := llb.NewDefinitionOp(fs.PB)
	if err != nil {
		return llb.State{}, err
	}
	return llb.NewState(defop), nil
}

// TODO: Evaluate needs to know which schema any query should be run against, put that inside FS (in a deterministic way to retain caching)
func (fs FS) Evaluate(ctx context.Context) (FS, error) {
	for fs.PB == nil {
		// TODO: guard against accidental infinite loop
		// this loop is where the "stack" is unwound, should later add debug info that traces each query leading to the final result
		if fs.Query == "" {
			return FS{}, fmt.Errorf("invalid fs: missing query")
		}
		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  fs.Query,
			Context:        withEval(ctx),
			VariableValues: fs.Vars,
		})
		if result.HasErrors() {
			return FS{}, fmt.Errorf("fs eval errors: %+v", result.Errors)
		}

		// Extract the queried field out of the result
		resultMap := result.Data.(map[string]interface{})
		req, err := parser.Parse(parser.ParseParams{Source: fs.Query})
		if err != nil {
			return FS{}, err
		}
		field := req.Definitions[0].(*ast.OperationDefinition).SelectionSet.Selections[0].(*ast.Field)
		for field.SelectionSet != nil {
			resultMap = resultMap[field.Name.Value].(map[string]interface{})
			field = field.SelectionSet.Selections[0].(*ast.Field)
		}
		rawFS, ok := resultMap[field.Name.Value].(string)
		if !ok {
			return FS{}, fmt.Errorf("invalid fs type %T", resultMap[field.Name.Value])
		}
		if err := fs.UnmarshalText([]byte(rawFS)); err != nil {
			return FS{}, err
		}
	}
	return fs, nil
}

// TODO: dedupe all the methods with equivalent in FS
type DaggerString struct {
	Value *string
	Query string
	Vars  map[string]interface{} // NOTE: encoding/json sorts by key so this *should* be deterministic (?)
}

func (s DaggerString) MarshalJSON() ([]byte, error) {
	a, err := s.MarshalAny()
	if err != nil {
		return nil, err
	}
	return json.Marshal(a)
}

func (s DaggerString) MarshalAny() (any, error) {
	if s.Value != nil {
		return s.Value, nil
	}
	type marshal DaggerString
	bytes, err := json.Marshal(marshal(s))
	if err != nil {
		return nil, err
	}
	return []any{
		base64.StdEncoding.EncodeToString(bytes),
	}, nil
}

func (s *DaggerString) UnmarshalJSON(data []byte) error {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	return s.UnmarshalAny(raw)
}

func (s *DaggerString) UnmarshalAny(data any) error {
	switch data := data.(type) {
	case string:
		s.Value = &data
	case []interface{}:
		if len(data) != 1 {
			return fmt.Errorf("invalid dagger string: %v", data)
		}
		raw, ok := data[0].(string)
		if !ok {
			return fmt.Errorf("invalid dagger string: %v", data)
		}
		bytes, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return err
		}
		type marshal DaggerString
		var result marshal
		if err := json.Unmarshal(bytes, &result); err != nil {
			return err
		}
		*s = DaggerString(result)
	default:
		return fmt.Errorf("invalid dagger string: %T(%+v)", data, data)
	}
	return nil
}

func (s DaggerString) Evaluate(ctx context.Context) (DaggerString, error) {
	for s.Value == nil {
		if s.Query == "" {
			return DaggerString{}, fmt.Errorf("invalid dagger string: missing query")
		}
		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  s.Query,
			Context:        withEval(ctx),
			VariableValues: s.Vars,
		})
		if result.HasErrors() {
			return DaggerString{}, fmt.Errorf("dagger string eval errors: %+v", result.Errors)
		}

		// Extract the queried field out of the result
		resultMap := result.Data.(map[string]interface{})
		req, err := parser.Parse(parser.ParseParams{Source: s.Query})
		if err != nil {
			return DaggerString{}, err
		}
		field := req.Definitions[0].(*ast.OperationDefinition).SelectionSet.Selections[0].(*ast.Field)
		for field.SelectionSet != nil {
			resultMap = resultMap[field.Name.Value].(map[string]interface{})
			field = field.SelectionSet.Selections[0].(*ast.Field)
		}
		if err := s.UnmarshalAny(resultMap[field.Name.Value]); err != nil {
			return DaggerString{}, err
		}
	}
	return s, nil
}

var coreSchema tools.ExecutableSchema

type evalKey struct{}

func withEval(ctx context.Context) context.Context {
	return context.WithValue(ctx, evalKey{}, true)
}

func shouldEval(ctx context.Context) bool {
	val, ok := ctx.Value(evalKey{}).(bool)
	return ok && val
}

/*
	type AlpineBuild {
		fs: FS!
	}
	type Query {
		build(pkgs: [String]!): AlpineBuild
	}

	converts to:

	type AlpineBuild {
		fs: FS!
	}
	type Alpine {
		build(pkgs: [String]!): AlpineBuild
	}
	type Query {
		alpine: Alpine!
	}
*/
func parseSchema(pkgName string, typeDefs string) (*tools.ExecutableSchema, error) {
	capName := strings.ToUpper(string(pkgName[0])) + pkgName[1:]
	resolverMap := tools.ResolverMap{
		"Query": &tools.ObjectResolver{
			Fields: tools.FieldResolveMap{
				pkgName: &tools.FieldResolve{
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return struct{}{}, nil
					},
				},
			},
		},
		capName: &tools.ObjectResolver{
			Fields: tools.FieldResolveMap{},
		},
	}

	doc, err := parser.Parse(parser.ParseParams{Source: typeDefs})
	if err != nil {
		return nil, err
	}

	var actions []string
	var otherObjects []string
	for _, def := range doc.Definitions {
		if def, ok := def.(*ast.ObjectDefinition); ok {
			if def.Name.Value == "Query" {
				for _, field := range def.Fields {
					actions = append(actions, printer.Print(field).(string))
					resolverMap[capName].(*tools.ObjectResolver).Fields[field.Name.Value] = &tools.FieldResolve{
						Resolve: actionFieldToResolver(pkgName, field.Name.Value),
					}
				}
			} else {
				otherObjects = append(otherObjects, printer.Print(def).(string))
			}
		}
	}

	return &tools.ExecutableSchema{
		TypeDefs: fmt.Sprintf(`
%s
type %s {
	%s
}
type Query {
	%s: %s!
}
	`, strings.Join(otherObjects, "\n"), capName, strings.Join(actions, "\n"), pkgName, capName),
		Resolvers: resolverMap,
	}, nil
}

func actionFieldToResolver(pkgName, actionName string) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		if !shouldEval(p.Context) {
			return lazyResolve(p)
		}

		imgref := fmt.Sprintf("localhost:5555/dagger:%s", pkgName)

		// TODO: remove silly if statement once all actions move to the new graphql server model
		if pkgName == "graphql_ts" || pkgName == "alpine" {
			// the action doesn't know we stitch its queries under the package name, patch the query we send to here
			queryOp := p.Info.Operation.(*ast.OperationDefinition)
			packageSelect := queryOp.SelectionSet.Selections[0].(*ast.Field)
			queryOp.SelectionSet.Selections = packageSelect.SelectionSet.Selections
			query := printer.Print(queryOp).(string)

			input := llb.Scratch().File(llb.Mkfile("/dagger.graphql", 0644, []byte(query)))
			st := llb.Image(imgref).Run(
				llb.Args([]string{"/entrypoint"}),
				llb.AddSSHSocket(
					llb.SSHID(daggerSockName),
					llb.SSHSocketTarget("/dagger.sock"),
				),
				llb.AddMount("/inputs", input, llb.Readonly),
				llb.ReadonlyRootFS(),
			)
			outputMnt := st.AddMount("/outputs", llb.Scratch())
			outputDef, err := outputMnt.Marshal(p.Context, llb.Platform(getPlatform(p)), llb.WithCustomName(fmt.Sprintf("%s.%s", pkgName, actionName)))
			if err != nil {
				return nil, err
			}
			gw, err := getGatewayClient(p)
			if err != nil {
				return nil, err
			}
			res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
				Evaluate:   true,
				Definition: outputDef.ToPB(),
			})
			if err != nil {
				return nil, err
			}
			ref, err := res.SingleRef()
			if err != nil {
				return nil, err
			}
			outputBytes, err := ref.ReadFile(p.Context, bkgw.ReadRequest{
				Filename: "/dagger.json",
			})
			if err != nil {
				return nil, err
			}
			fmt.Printf("%s.%s output: %s\n", pkgName, actionName, string(outputBytes))
			var output interface{}
			if err := json.Unmarshal(outputBytes, &output); err != nil {
				return nil, fmt.Errorf("failed to unmarshal output: %w", err)
			}
			for _, parentField := range append([]any{"data"}, p.Info.Path.AsArray()[1:]...) { // skip first field, which is the package name
				outputMap, ok := output.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("output is not a map: %+v", output)
				}
				output = outputMap[parentField.(string)]
			}
			return output, nil
		} else {
			inputBytes, err := json.Marshal(p.Args)
			if err != nil {
				return nil, err
			}
			input := llb.Scratch().File(llb.Mkfile("/dagger.json", 0644, inputBytes))
			st := llb.Image(imgref).Run(
				llb.Args([]string{"/entrypoint", "-a", actionName}),
				llb.AddSSHSocket(
					llb.SSHID(daggerSockName),
					llb.SSHSocketTarget("/dagger.sock"),
				),
				llb.AddMount("/inputs", input, llb.Readonly),
				llb.ReadonlyRootFS(),
				llb.AddEnv("DAGGER_ACTION", actionName),
			)
			outputMnt := st.AddMount("/outputs", llb.Scratch())
			outputDef, err := outputMnt.Marshal(p.Context, llb.Platform(getPlatform(p)), llb.WithCustomName(fmt.Sprintf("%s.%s", pkgName, actionName)))
			if err != nil {
				return nil, err
			}
			gw, err := getGatewayClient(p)
			if err != nil {
				return nil, err
			}
			res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
				Evaluate:   true,
				Definition: outputDef.ToPB(),
			})
			if err != nil {
				return nil, err
			}
			ref, err := res.SingleRef()
			if err != nil {
				return nil, err
			}
			outputBytes, err := ref.ReadFile(p.Context, bkgw.ReadRequest{
				Filename: "/dagger.json",
			})
			if err != nil {
				return nil, err
			}
			var output interface{}
			if err := json.Unmarshal(outputBytes, &output); err != nil {
				return nil, fmt.Errorf("failed to unmarshal output: %w", err)
			}
			return output, nil
		}
	}
}

func lazyResolve(p graphql.ResolveParams) (interface{}, error) {
	var parentFieldNames []string
	for _, parent := range p.Info.Path.AsArray() {
		parentFieldNames = append(parentFieldNames, parent.(string))
	}
	lazyResult, err := getLazyResult(p,
		p.Info.ReturnType,
		parentFieldNames,
		p.Info.FieldASTs[0].SelectionSet,
	)
	if err != nil {
		return nil, err
	}
	return lazyResult, nil
}

func getLazyResult(p graphql.ResolveParams, output graphql.Output, parentFieldNames []string, selectionSet *ast.SelectionSet) (interface{}, error) {
	switch outputType := graphql.GetNullable(output).(type) {
	case *graphql.Scalar:
		selectedQuery, err := queryWithSelections(p.Info.Operation.(*ast.OperationDefinition), parentFieldNames)
		if err != nil {
			return nil, err
		}
		switch outputType.Name() {
		case "FS":
			bytes, err := FS{
				Query: printer.Print(selectedQuery).(string),
				Vars:  p.Info.VariableValues,
			}.MarshalText()
			if err != nil {
				return nil, err
			}
			return string(bytes), nil
		case "DaggerString":
			return DaggerString{
				Query: printer.Print(selectedQuery).(string),
				Vars:  p.Info.VariableValues,
			}.MarshalAny()
		default:
			return nil, fmt.Errorf("FIXME: currently unsupported scalar output type %s", outputType.Name())
		}
		// TODO: case *graphql.List: (may need to model lazy list using pagination)
	case *graphql.Object:
		result := make(map[string]interface{})
		for fieldName, field := range outputType.Fields() {
			// Check if this field is actually being selected, skip if not
			var selection *ast.Field
			for _, s := range selectionSet.Selections {
				s := s.(*ast.Field)
				if s.Name.Value == fieldName {
					selection = s
					break
				}
			}
			if selection == nil {
				continue
			}
			// Recurse to the selected field
			fieldNames := make([]string, len(parentFieldNames))
			copy(fieldNames, parentFieldNames)
			fieldNames = append(fieldNames, fieldName)
			subResult, err := getLazyResult(p, field.Type, fieldNames, selection.SelectionSet)
			if err != nil {
				return nil, err
			}
			result[fieldName] = subResult
		}
		return result, nil
	default:
		return nil, fmt.Errorf("FIXME: currently unsupported output type %T", output)
	}
}

func queryWithSelections(query *ast.OperationDefinition, fieldNames []string) (*ast.OperationDefinition, error) {
	newQuery := *query
	var err error
	newQuery.SelectionSet, err = filterSelectionSets(query.SelectionSet, fieldNames)
	if err != nil {
		return nil, err
	}
	return &newQuery, nil
}

func filterSelectionSets(selectionSet *ast.SelectionSet, fieldNames []string) (*ast.SelectionSet, error) {
	selectionSet, err := copySelectionSet(selectionSet)
	if err != nil {
		return nil, err
	}
	curSelectionSet := selectionSet
	for _, fieldName := range fieldNames {
		newSelectionSet, err := filterSelectionSet(curSelectionSet, fieldName)
		if err != nil {
			return nil, err
		}
		curSelectionSet.Selections = newSelectionSet.Selections
		curSelectionSet = newSelectionSet.Selections[0].(*ast.Field).SelectionSet
	}
	return selectionSet, nil
}

// return the selection set where the provided field is the only selection
func filterSelectionSet(selectionSet *ast.SelectionSet, fieldName string) (*ast.SelectionSet, error) {
	matchIndex := -1
	for i, selection := range selectionSet.Selections {
		selection := selection.(*ast.Field)
		if selection.Name.Value == fieldName {
			matchIndex = i
			break
		}
	}
	if matchIndex == -1 {
		return nil, fmt.Errorf("could not find %s in selectionSet %s", fieldName, printer.Print(selectionSet).(string))
	}
	selectionSet.Selections = []ast.Selection{selectionSet.Selections[matchIndex]}
	return selectionSet, nil
}

func copySelectionSet(selectionSet *ast.SelectionSet) (*ast.SelectionSet, error) {
	if selectionSet == nil {
		return nil, nil
	}
	var selections []ast.Selection
	for _, selection := range selectionSet.Selections {
		field, ok := selection.(*ast.Field)
		if !ok {
			return nil, fmt.Errorf("unsupported selection type %T", selection)
		}
		newField, err := copyField(field)
		if err != nil {
			return nil, err
		}
		selections = append(selections, newField)
	}
	return &ast.SelectionSet{Kind: selectionSet.Kind, Loc: selectionSet.Loc, Selections: selections}, nil
}

func copyField(field *ast.Field) (*ast.Field, error) {
	newField := *field
	var err error
	newField.SelectionSet, err = copySelectionSet(field.SelectionSet)
	if err != nil {
		return nil, err
	}
	return &newField, nil
}

// TODO: shouldn't be global vars, pass through resolve context, make sure synchronization is handled, etc.
var schema graphql.Schema
var pkgSchemas map[string]tools.ExecutableSchema

func reloadSchemas() error {
	// tools.MakeExecutableSchema doesn't actually merge multiple schemas, it just overwrites any
	// overlapping types This is fine for the moment except for the Query type, which we need to be an
	// actual merge, so we do that ourselves here by merging the Query resolvers and appending a merged
	// Query type to the typeDefs.
	var queryFields []string
	var otherObjects []string
	for _, pkgSchema := range pkgSchemas {
		doc, err := parser.Parse(parser.ParseParams{Source: pkgSchema.TypeDefs})
		if err != nil {
			return err
		}
		for _, def := range doc.Definitions {
			if def, ok := def.(*ast.ObjectDefinition); ok {
				if def.Name.Value == "Query" {
					for _, field := range def.Fields {
						queryFields = append(queryFields, printer.Print(field).(string))
					}
					continue
				}
			}
			otherObjects = append(otherObjects, printer.Print(def).(string))
		}
	}

	resolvers := make(map[string]interface{})
	for _, pkgSchema := range pkgSchemas {
		for k, v := range pkgSchema.Resolvers {
			// TODO: need more general solution, verification that overwrites aren't happening, etc.
			if k == "Query" {
				if existing, ok := resolvers[k]; ok {
					existing := existing.(*tools.ObjectResolver)
					for field, fieldResolver := range v.(*tools.ObjectResolver).Fields {
						existing.Fields[field] = fieldResolver
					}
					v = existing
				}
			}
			resolvers[k] = v
		}
	}

	typeDefs := fmt.Sprintf(`
%s
type Query {
  %s
}
	`, strings.Join(otherObjects, "\n"), strings.Join(queryFields, "\n "))

	var err error
	schema, err = tools.MakeExecutableSchema(tools.ExecutableSchema{
		TypeDefs:  typeDefs,
		Resolvers: resolvers,
	})
	if err != nil {
		return err
	}

	return nil
}

func init() {
	pkgSchemas = make(map[string]tools.ExecutableSchema)
	pkgSchemas["core"] = tools.ExecutableSchema{
		TypeDefs: `
scalar FS
scalar DaggerString

type CoreImage {
	fs: FS!
}

input CoreMount {
	path: String!
	fs: FS!
}
input CoreExecInput {
	mounts: [CoreMount!]!
	args: [String!]!
}
type CoreExecOutput {
	mount(path: String!): FS
}
type CoreExec {
	fs: FS!
}

type Core {
	image(ref: String!): CoreImage
	# exec(input: CoreExecInput!): CoreExecOutput
	exec(fs: FS!, args: [String]!): CoreExec
}

type Query {
	core: Core!
}

type Package {
	name: String!
}

type Mutation {
	import(ref: String!): Package
	evaluate(fs: FS!): FS
	readfile(fs: FS!, path: String!): String
	clientdir(id: String!): FS
}
		`,
		Resolvers: tools.ResolverMap{
			"Query": &tools.ObjectResolver{
				Fields: tools.FieldResolveMap{
					"core": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							return struct{}{}, nil
						},
					},
				},
			},
			"CoreExecOutput": &tools.ObjectResolver{
				Fields: tools.FieldResolveMap{
					"mount": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							if lazyOutput, ok := p.Source.(map[string]interface{}); ok {
								return lazyOutput["mount"], nil
							}
							fsOutputs, ok := p.Source.(map[string]string)
							if !ok {
								return nil, fmt.Errorf("unexpected core exec source type %T", p.Source)
							}
							rawPath, ok := p.Args["path"]
							if !ok {
								return nil, fmt.Errorf("missing path argument")
							}
							path, ok := rawPath.(string)
							if !ok {
								return nil, fmt.Errorf("path argument is not a string")
							}
							fsstr, ok := fsOutputs[path]
							if !ok {
								return nil, fmt.Errorf("mount at path %q not found", path)
							}
							return fsstr, nil
						},
					},
				},
			},
			"Core": &tools.ObjectResolver{
				Fields: tools.FieldResolveMap{
					"image": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							if !shouldEval(p.Context) {
								return lazyResolve(p)
							}
							rawRef, ok := p.Args["ref"]
							if !ok {
								return nil, fmt.Errorf("missing ref")
							}
							/* TODO: switch back to DaggerString once re-integrated with generated clients
							var ref DaggerString
							if err := ref.UnmarshalAny(rawRef); err != nil {
								return nil, err
							}
							ref, err := ref.Evaluate(p.Context)
							if err != nil {
								return nil, fmt.Errorf("error evaluating image ref: %v", err)
							}
							*/
							ref, ok := rawRef.(string)
							if !ok {
								return nil, fmt.Errorf("ref is not a string")
							}
							// llbdef, err := llb.Image(*ref.Value).Marshal(p.Context, llb.Platform(getPlatform(p)))
							llbdef, err := llb.Image(ref).Marshal(p.Context, llb.Platform(getPlatform(p)))
							if err != nil {
								return nil, err
							}
							fsbytes, err := FS{PB: llbdef.ToPB()}.MarshalText()
							if err != nil {
								return nil, err
							}
							return map[string]interface{}{
								"fs": string(fsbytes),
							}, nil
						},
					},
					"exec": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							if !shouldEval(p.Context) {
								return lazyResolve(p)
							}
							rawFS, ok := p.Args["fs"].(string)
							if !ok {
								return nil, fmt.Errorf("invalid fs")
							}
							var fs FS
							if err := fs.UnmarshalText([]byte(rawFS)); err != nil {
								return nil, err
							}
							rawArgs, ok := p.Args["args"].([]interface{})
							if !ok {
								return nil, fmt.Errorf("invalid args")
							}
							var args []string
							for _, arg := range rawArgs {
								if arg, ok := arg.(string); ok {
									args = append(args, arg)
								} else {
									return nil, fmt.Errorf("invalid arg")
								}
							}
							fs, err := fs.Evaluate(p.Context)
							if err != nil {
								return nil, err
							}
							fsState, err := fs.ToState()
							if err != nil {
								return nil, err
							}
							llbdef, err := fsState.Run(llb.Args(args)).Root().Marshal(p.Context, llb.Platform(getPlatform(p)))
							if err != nil {
								return nil, err
							}
							bytes, err := FS{PB: llbdef.ToPB()}.MarshalText()
							if err != nil {
								return nil, err
							}
							return map[string]interface{}{
								"fs": string(bytes),
							}, nil
						},
					},
				},
				// 		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				// 			if !shouldEval(p.Context) {
				// 				return lazyResolve(p)
				// 			}
				// 			input, ok := p.Args["input"].(map[string]interface{})
				// 			if !ok {
				// 				return nil, fmt.Errorf("invalid fs")
				// 			}

				// 			rawMounts, ok := input["mounts"].([]interface{})
				// 			if !ok {
				// 				return nil, fmt.Errorf("invalid mounts")
				// 			}
				// 			inputMounts := make(map[string]FS)
				// 			for _, rawMount := range rawMounts {
				// 				mount, ok := rawMount.(map[string]interface{})
				// 				if !ok {
				// 					return nil, fmt.Errorf("invalid mount: %T", rawMount)
				// 				}
				// 				path, ok := mount["path"].(string)
				// 				if !ok {
				// 					return nil, fmt.Errorf("invalid mount path")
				// 				}
				// 				path = filepath.Clean(path)
				// 				fsstr, ok := mount["fs"].(string)
				// 				if !ok {
				// 					return nil, fmt.Errorf("invalid mount fs")
				// 				}
				// 				var fs FS
				// 				if err := fs.UnmarshalText([]byte(fsstr)); err != nil {
				// 					return nil, err
				// 				}
				// 				inputMounts[path] = fs
				// 			}
				// 			rootFS, ok := inputMounts["/"]
				// 			if !ok {
				// 				return nil, fmt.Errorf("missing root fs")
				// 			}

				// 			rawArgs, ok := input["args"].([]interface{})
				// 			if !ok {
				// 				return nil, fmt.Errorf("invalid args")
				// 			}
				// 			var args []string
				// 			for _, rawArg := range rawArgs {
				// 				/* TODO: switch back to DaggerString once re-integrated with generated clients
				// 				var arg DaggerString
				// 				if err := arg.UnmarshalAny(rawArg); err != nil {
				// 					return nil, fmt.Errorf("invalid arg: %w", err)
				// 				}
				// 				arg, err := arg.Evaluate(p.Context)
				// 				if err != nil {
				// 					return nil, fmt.Errorf("error evaluating arg: %v", err)
				// 				}
				// 				args = append(args, *arg.Value)
				// 				*/
				// 				arg, ok := rawArg.(string)
				// 				if !ok {
				// 					return nil, fmt.Errorf("invalid arg: %T", rawArg)
				// 				}
				// 				args = append(args, arg)
				// 			}

				// 			rootFS, err := rootFS.Evaluate(p.Context)
				// 			if err != nil {
				// 				return nil, err
				// 			}
				// 			state, err := rootFS.ToState()
				// 			if err != nil {
				// 				return nil, err
				// 			}
				// 			execState := state.Run(llb.Args(args))
				// 			outputStates := map[string]llb.State{
				// 				"/": execState.Root(),
				// 			}
				// 			for path, inputFS := range inputMounts {
				// 				if path == "/" {
				// 					continue
				// 				}
				// 				inputFS, err := inputFS.Evaluate(p.Context)
				// 				if err != nil {
				// 					return nil, err
				// 				}
				// 				inputState, err := inputFS.ToState()
				// 				if err != nil {
				// 					return nil, err
				// 				}
				// 				outputStates[path] = execState.AddMount(path, inputState)
				// 			}
				// 			fsOutputs := make(map[string]string)
				// 			for path, outputState := range outputStates {
				// 				llbdef, err := outputState.Marshal(p.Context, llb.Platform(getPlatform(p)))
				// 				if err != nil {
				// 					return nil, err
				// 				}
				// 				fsbytes, err := FS{PB: llbdef.ToPB()}.MarshalText()
				// 				if err != nil {
				// 					return nil, err
				// 				}
				// 				fsOutputs[path] = string(fsbytes)
				// 			}
				// 			return fsOutputs, nil
				// 		},
				// 	},
				// },
			},

			"Mutation": &tools.ObjectResolver{
				Fields: tools.FieldResolveMap{
					"import": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							// TODO: make sure not duped
							pkgName, ok := p.Args["ref"].(string)
							if !ok {
								return nil, fmt.Errorf("invalid ref")
							}
							imgref := fmt.Sprintf("localhost:5555/dagger:%s", pkgName)

							pkgDef, err := llb.Image(imgref).Marshal(p.Context)
							if err != nil {
								return nil, err
							}
							gw, err := getGatewayClient(p)
							if err != nil {
								return nil, err
							}
							res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
								Evaluate:   true,
								Definition: pkgDef.ToPB(),
							})
							if err != nil {
								return nil, err
							}
							bkref, err := res.SingleRef()
							if err != nil {
								return nil, err
							}
							outputBytes, err := bkref.ReadFile(p.Context, bkgw.ReadRequest{
								Filename: "/dagger.graphql",
							})
							if err != nil {
								return nil, err
							}
							parsed, err := parseSchema(pkgName, string(outputBytes))
							if err != nil {
								return nil, err
							}
							pkgSchemas[pkgName] = *parsed

							if err := reloadSchemas(); err != nil {
								return nil, err
							}
							return map[string]interface{}{
								"name": pkgName,
							}, nil
						},
					},
					"evaluate": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							fsstr, ok := p.Args["fs"].(string)
							if !ok {
								return nil, fmt.Errorf("invalid fs")
							}
							var fs FS
							if err := fs.UnmarshalText([]byte(fsstr)); err != nil {
								return nil, err
							}
							fs, err := fs.Evaluate(p.Context)
							if err != nil {
								return nil, fmt.Errorf("failed to evaluate fs: %v", err)
							}
							gw, err := getGatewayClient(p)
							if err != nil {
								return nil, err
							}
							_, err = gw.Solve(context.Background(), bkgw.SolveRequest{
								Evaluate:   true,
								Definition: fs.PB,
							})
							if err != nil {
								return nil, err
							}
							fsbytes, err := fs.MarshalText()
							if err != nil {
								return nil, err
							}
							return string(fsbytes), nil
						},
					},
					"readfile": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							fsstr, ok := p.Args["fs"].(string)
							if !ok {
								return nil, fmt.Errorf("invalid fs")
							}
							var fs FS
							if err := fs.UnmarshalText([]byte(fsstr)); err != nil {
								return nil, err
							}
							path, ok := p.Args["path"].(string)
							if !ok {
								return nil, fmt.Errorf("invalid path")
							}
							fs, err := fs.Evaluate(p.Context)
							if err != nil {
								return nil, err
							}
							gw, err := getGatewayClient(p)
							if err != nil {
								return nil, err
							}
							res, err := gw.Solve(context.Background(), bkgw.SolveRequest{
								Evaluate:   true,
								Definition: fs.PB,
							})
							if err != nil {
								return nil, err
							}
							ref, err := res.SingleRef()
							if err != nil {
								return nil, err
							}
							outputBytes, err := ref.ReadFile(p.Context, bkgw.ReadRequest{
								Filename: path,
							})
							if err != nil {
								return nil, err
							}
							return string(outputBytes), nil
						},
					},
					"readstring": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							var str DaggerString
							if err := str.UnmarshalAny(p.Args["str"]); err != nil {
								return nil, err
							}
							str, err := str.Evaluate(p.Context)
							if err != nil {
								return nil, fmt.Errorf("failed to evaluate dagger string: %v", err)
							}
							return str.Value, nil
						},
					},
					"clientdir": &tools.FieldResolve{
						Resolve: func(p graphql.ResolveParams) (interface{}, error) {
							id, ok := p.Args["id"].(string)
							if !ok {
								return nil, fmt.Errorf("invalid clientdir id")
							}
							llbdef, err := llb.Local(id).Marshal(p.Context)
							if err != nil {
								return nil, err
							}
							fsbytes, err := FS{PB: llbdef.ToPB()}.MarshalText()
							if err != nil {
								return nil, err
							}
							return string(fsbytes), nil
						},
					},
				},
			},
			"FS": &tools.ScalarResolver{
				Serialize: func(value interface{}) interface{} {
					return value
				},
				ParseValue: func(value interface{}) interface{} {
					return value
				},
				ParseLiteral: func(valueAST ast.Value) interface{} {
					switch valueAST := valueAST.(type) {
					case *ast.StringValue:
						return valueAST.Value
					default:
						panic(fmt.Sprintf("unsupported fs type: %T", valueAST))
					}
				},
			},
			"DaggerString": &tools.ScalarResolver{
				Serialize: func(value interface{}) interface{} {
					return value
				},
				ParseValue: func(value interface{}) interface{} {
					return value
				},
				ParseLiteral: func(valueAST ast.Value) interface{} {
					switch valueAST := valueAST.(type) {
					case *ast.StringValue:
						return valueAST.Value
					case *ast.ListValue:
						if len(valueAST.Values) != 1 {
							panic(fmt.Sprintf("invalid dagger string: %+v", valueAST.Values))
						}
						elem, ok := valueAST.Values[0].(*ast.StringValue)
						if !ok {
							panic(fmt.Sprintf("invalid dagger string: %+v", valueAST.Values))
						}
						return []any{elem.Value}
					default:
						panic(fmt.Sprintf("unsupported fs type: %T", valueAST))
					}
				},
			},
		},
	}

	if err := reloadSchemas(); err != nil {
		panic(err)
	}
}

type gatewayClientKey struct{}

func withGatewayClient(ctx context.Context, gw bkgw.Client) context.Context {
	return context.WithValue(ctx, gatewayClientKey{}, gw)
}

func getGatewayClient(p graphql.ResolveParams) (bkgw.Client, error) {
	v := p.Context.Value(gatewayClientKey{})
	if v == nil {
		return nil, fmt.Errorf("no gateway client")
	}
	return v.(bkgw.Client), nil
}

type platformKey struct{}

func withPlatform(ctx context.Context, platform *specs.Platform) context.Context {
	return context.WithValue(ctx, platformKey{}, platform)
}

func getPlatform(p graphql.ResolveParams) specs.Platform {
	v := p.Context.Value(platformKey{})
	if v == nil {
		return platforms.DefaultSpec()
	}
	return *v.(*specs.Platform)
}

func getQuery(p graphql.ResolveParams) string {
	return printer.Print(p.Info.Operation).(string)
}
