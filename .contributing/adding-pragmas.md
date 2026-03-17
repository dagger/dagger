## How to Add a New Pragma and GraphQL Directive

### Overview
Adding a pragma (like `+myFeature`) and its associated GraphQL directive (like `@myFeature`) requires changes across 6 layers of the codebase. Here's the complete workflow:

---

### 1. **Define the GraphQL Directive** (dagql/server.go)

Add your directive to the `coreDirectives` array (~line 304):

```go
{
    Name:        "myFeature",
    Description: FormatDescription(`Description of what this directive does.`),
    Args: NewInputSpecs(
        InputSpec{
            Name: "arg1",
            Type: String(""),
        },
        InputSpec{
            Name: "arg2",
            Type: ArrayInput[String](nil),  // for array args
        },
    ),
    Locations: []DirectiveLocation{
        DirectiveLocationArgumentDefinition,  // for function arguments
        // or DirectiveLocationFieldDefinition for fields, etc.
    },
},
```

Set the appropriate DirectiveLocation based on whether the pragma goes on a
FUNCTION (GraphQL field) or an ARGUMENT.

---

### 2. **Add Field to FunctionArg** (core/typedef.go)

If the pragma is on an ARGUMENT, add your field to the `FunctionArg` struct (~line 296):

```go
type FunctionArg struct {
    Name         string
    Description  string
    // ... existing fields ...
    MyFeature    string   `field:"true" doc:"Description of my feature"`
    MyFeatureList []string `field:"true" doc:"List for my feature"`
    // ... other fields ...
}
```

---

### 3. **Add Directive Generation** (core/typedef.go)

Update the `Function.Directives()` or `FunctionArg.Directives()` method:

```go
func (arg FunctionArg) Directives() []*ast.Directive {
    var directives []*ast.Directive

    // ... existing directives ...

    if arg.MyFeature != "" {
        directives = append(directives, &ast.Directive{
            Name: "myFeature",
            Arguments: ast.ArgumentList{
                {
                    Name: "arg1",
                    Value: &ast.Value{
                        Kind: ast.StringValue,
                        Raw:  arg.MyFeature,
                    },
                },
            },
        })
    }

    if len(arg.MyFeatureList) > 0 {
        var children ast.ChildValueList
        for _, item := range arg.MyFeatureList {
            children = append(children, &ast.ChildValue{
                Value: &ast.Value{
                    Kind: ast.StringValue,
                    Raw:  item,
                },
            })
        }
        directives = append(directives, &ast.Directive{
            Name: "myFeature",
            Arguments: ast.ArgumentList{
                &ast.Argument{
                    Name: "arg2",
                    Value: &ast.Value{
                        Kind:     ast.ListValue,
                        Children: children,
                    },
                },
            },
        })
    }

    return directives
}
```

---

### 4. **Update Function.WithArg** (core/typedef.go)

If the pragma is on an ARGUMENT, dd your parameter to the `WithArg` method (~line 186):

```go
func (fn *Function) WithArg(name string, typeDef *TypeDef, desc string,
                           defaultValue JSON, defaultPath string, ignore []string,
                           myFeature string, myFeatureList []string,  // ADD HERE
                           sourceMap *SourceMap, deprecated *string) *Function {
    fn = fn.Clone()
    arg := &FunctionArg{
        Name:          strcase.ToLowerCamel(name),
        Description:   desc,
        TypeDef:       typeDef,
        DefaultValue:  defaultValue,
        DefaultPath:   defaultPath,
        Ignore:        ignore,
        MyFeature:     myFeature,      // ADD HERE
        MyFeatureList: myFeatureList,  // ADD HERE
        Deprecated:    deprecated,
        OriginalName:  name,
    }
    // ... rest of method ...
}
```

---

### 5. **Add GraphQL Resolver** (core/schema/module.go)

If the pragma is on a FUNCTION, add a new API field and method:

```go
func (s *moduleSchema) Install(dag *dagql.Server) {
	// ...
	dagql.Fields[*core.Function]{
		// ...

		// ADD API RESOLVER
		dagql.Func("withCheck", s.functionWithCheck).
					Doc(`Returns the function with a flag indicating it's a check.`),
	}.Install(dag)
}

// ...

// ADD API RESOLVER CALLBACK
func (s *moduleSchema) functionWithMyFeature(ctx context.Context, fn *core.Function, args struct {
	MyPragmaFlag bool `default:"true"`
}) (*core.Function, error) {
	return fn.WithMyFeature(args.MyPragmaFlag), nil
}
```

If the pragma is on an ARGUMENT, update the `functionWithArg` method (~line 516):

```go
func (s *moduleSchema) functionWithArg(ctx context.Context, fn *core.Function, args struct {
    Name         string
    TypeDef      core.TypeDefID
    Description  string    `default:""`
    DefaultValue core.JSON `default:""`
    DefaultPath  string    `default:""`
    Ignore       []string  `default:"[]"`
    MyFeature    string    `default:""`      // ADD HERE
    MyFeatureList []string `default:"[]"`    // ADD HERE
    SourceMap    dagql.Optional[core.SourceMapID]
    Deprecated   *string
}) (*core.Function, error) {
    // ... validation logic ...

    return fn.WithArg(args.Name, td, args.Description, args.DefaultValue,
                     args.DefaultPath, args.Ignore,
                     args.MyFeature, args.MyFeatureList,  // ADD HERE
                     sourceMap, args.Deprecated), nil
}
```

---

### 6. **Re-generate the SDKs**

Since there are new APIs, you will need to re-generate the SDK client code to use it.

---

### 7. **Add Go SDK Pragma Parsing** (cmd/codegen/generator/go/templates/module_funcs.go)

Update the `parseParamSpecVar` method (~line 370):

```go
// Parse pragma from comments
myFeature := ""
if v, ok := pragmas["myFeature"]; ok {
    myFeature, ok = v.(string)
    if !ok {
        return paramSpec{}, fmt.Errorf("myFeature pragma %q, must be a valid string", v)
    }
}

myFeatureList := []string{}
if v, ok := pragmas["myFeatureList"]; ok {
    err := mapstructure.Decode(v, &myFeatureList)
    if err != nil {
        return paramSpec{}, fmt.Errorf("myFeatureList pragma %q, must be a valid JSON array: %w", v, err)
    }
}
```

Add fields to `paramSpec` struct (~line 433):

```go
type paramSpec struct {
    // ... existing fields ...
    myFeature     string
    myFeatureList []string
}
```

Update the `TypeDefFunc` method (~line 180):

```go
if argSpec.myFeature != "" {
    argOpts.MyFeature = argSpec.myFeature
}

if len(argSpec.myFeatureList) > 0 {
    argOpts.MyFeatureList = argSpec.myFeatureList
}

fnTypeDef = fnTypeDef.WithArg(argSpec.name, argTypeDef, argOpts)
```

---

### 8. **Add to dagger.FunctionWithArgOpts** (SDK generation)

The SDK will automatically regenerate `FunctionWithArgOpts` when you run codegen, adding your new fields.

---

## Example Usage (After Implementation)

Once implemented, users can use the pragma in their Go modules:

```go
func (m *MyModule) MyFunction(
    ctx context.Context,
    // Use the pragma
    // +myFeature=value1
    // +myFeatureList=["item1", "item2"]
    input string,
) string {
    // ...
}
```

---

## Key Pattern Notes

1. **Naming**: Directive names use camelCase (`myFeature`), pragma names match (`+myFeature`)
2. **Field tags**: Core types use `` `field:"true" doc:"..."` `` for GraphQL exposure
3. **Array handling**: Use `ast.ChildValueList` for list values in directives
4. **Optional fields**: Use `default:""` or `default:"[]"` in resolver struct tags
5. **Pragma parsing**: Uses regex `pragmaCommentRegexp` to extract `+key=value` from comments
6. **JSON decoding**: Complex values (arrays, objects) use `json.NewDecoder` or `mapstructure.Decode`

---

## Reference Commits

- `d8e77997d` - Added `@defaultPath` directive
- `31a84b7ff` - Added `@ignorePatterns` directive
- `a499195df` - Added Go SDK pragma parsing for `+default` and `+optional`
