package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/dagger/dagger/core"
)

func main() {
	var err error

	ctx := context.Background()

	t := &core.TypeDef{}
	t = t.WithObject("PrintModule", "PrintModule desc", &core.SourceMap{Filename: "main.go", Line: 8, Column: 6})
	t, err = t.WithFunction(core.NewFunction("ContainerEcho",
		(&core.TypeDef{}).WithObject("Container", "", nil)).
		WithSourceMap(&core.SourceMap{Filename: "main.go", Line: 10, Column: 1}).
		WithArg("stringArg", (&core.TypeDef{}).WithKind(core.TypeDefKindString), "", core.JSON(""), "", nil, nil))
	if err != nil {
		log.Fatal(err)
	}

	m := &core.Module{}
	m, err = m.WithObject(ctx, t)
	if err != nil {
		log.Fatal(err)
	}

	str, err := m.ToJSONString()
	if err != nil {
		log.Fatal(err)
	}

	prettyPrint(str)

	/*
		Generated JSON:

			{
			  "description": "",
			  "enums": [],
			  "interfaces": [],
			  "name": "",
			  "objects": [
			    {
			      "kind": "OBJECT_KIND",
			      "optional": false,
			      "values": {
			        "Constructor": null,
			        "Description": "PrintModule desc",
			        "Fields": [],
			        "Functions": [
			          {
			            "Args": [
			              {
			                "DefaultPath": "",
			                "DefaultValue": "",
			                "Description": "",
			                "Ignore": null,
			                "Name": "stringArg",
			                "OriginalName": "stringArg",
			                "SourceMap": null,
			                "TypeDef": {
			                  "kind": "STRING_KIND",
			                  "optional": false
			                }
			              }
			            ],
			            "Description": "",
			            "Name": "containerEcho",
			            "OriginalName": "ContainerEcho",
			            "ParentOriginalName": "PrintModule",
			            "ReturnType": {
			              "kind": "OBJECT_KIND",
			              "optional": false,
			              "values": {
			                "Constructor": null,
			                "Description": "",
			                "Fields": [],
			                "Functions": [],
			                "Name": "Container",
			                "OriginalName": "Container",
			                "SourceMap": null,
			                "SourceModuleName": ""
			              }
			            },
			            "SourceMap": {
			              "Column": 1,
			              "Filename": "main.go",
			              "Line": 10,
			              "Module": ""
			            }
			          }
			        ],
			        "Name": "PrintModule",
			        "OriginalName": "PrintModule",
			        "SourceMap": {
			          "Column": 6,
			          "Filename": "main.go",
			          "Line": 8,
			          "Module": ""
			        },
			        "SourceModuleName": ""
			      }
			    }
			  ],
			  "originalName": ""
			}
	*/
}

func prettyPrint(jsonStr string) {
	var prettyJSON map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &prettyJSON); err == nil {
		prettyBytes, _ := json.MarshalIndent(prettyJSON, "", "  ")
		fmt.Printf("%s\n", string(prettyBytes))
	} else {
		fmt.Printf("%s\n", jsonStr)
	}
}

func printTypeDefJSON(name string, typeDef *core.TypeDef) {
	jsonStr, err := typeDef.ToJSONString()
	if err != nil {
		log.Printf("Error serializing %s: %v", name, err)
		return
	}

	// Pretty print the JSON
	var prettyJSON map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &prettyJSON); err == nil {
		prettyBytes, _ := json.MarshalIndent(prettyJSON, "", "  ")
		fmt.Printf("%s:\n%s\n", name, string(prettyBytes))
	} else {
		fmt.Printf("%s: %s\n", name, jsonStr)
	}
}

func testRoundTrip(original *core.TypeDef) {
	// Serialize to JSON
	jsonStr, err := original.ToJSONString()
	if err != nil {
		log.Printf("Error serializing: %v", err)
		return
	}

	// Deserialize back to TypeDef
	reconstructed, err := core.TypeDefFromJSONString(jsonStr)
	if err != nil {
		log.Printf("Error deserializing: %v", err)
		return
	}

	// Verify round-trip
	if original.Kind == reconstructed.Kind && original.Optional == reconstructed.Optional {
		fmt.Printf("✓ Round-trip successful for %s type\n", original.Kind)
	} else {
		fmt.Printf("✗ Round-trip failed for %s type\n", original.Kind)
	}
}

func printAllTypeDefKinds() {
	kinds := []core.TypeDefKind{
		core.TypeDefKindString,
		core.TypeDefKindInteger,
		core.TypeDefKindFloat,
		core.TypeDefKindBoolean,
		core.TypeDefKindScalar,
		core.TypeDefKindList,
		core.TypeDefKindObject,
		core.TypeDefKindInterface,
		core.TypeDefKindInput,
		core.TypeDefKindVoid,
		core.TypeDefKindEnum,
	}

	for _, kind := range kinds {
		fmt.Printf("- %s\n", kind)
	}
}
