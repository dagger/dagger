package schemadiff

import (
	"reflect"

	"github.com/josephburnett/jd/v2"
)

// This function takes four params (in json string format):
// 1. base schema with version-a of engine
// 2. base schema with version-b of engine
// 3. schema with requested module installed with version-a of engine
// 4. schema with requested module installed with version-b of engine
//
// It first calculate the diff between base schema's of version-a and version-b
// and then calculate the diff between full schema (with module installed) of version-a and version-b.
// Then it verifies if the full schema diff has more changes after ignoring the base schema diff,
// which essentially is the schema diff for the module.
//
// In order to ignore that diff from base schema, it normalizes a couple of things in the jd.DiffElement object:
// 1. It replaces all the jd.PathElement of type jd.PathIndex with constant value of 0 (basically ignoring index)
// 2. It removes the Before and After context from the diff. This context is useful when rendering the diff but ignored here.
func Do(baseSchemaA, baseSchemaB, withModuleSchemaA, withModuleSchemaB string) (string, error) {
	baseSchemaAVal, err := jd.ReadJsonString(baseSchemaA)
	if err != nil {
		return "", err
	}

	baseSchemaBVal, err := jd.ReadJsonString(baseSchemaB)
	if err != nil {
		return "", err
	}

	withModuleSchemaAVal, err := jd.ReadJsonString(withModuleSchemaA)
	if err != nil {
		return "", err
	}

	withModuleSchemaBVal, err := jd.ReadJsonString(withModuleSchemaB)
	if err != nil {
		return "", err
	}

	baseDiff := baseSchemaAVal.Diff(baseSchemaBVal)
	fullDiff := withModuleSchemaAVal.Diff(withModuleSchemaBVal)

	return comparePatch(baseDiff, fullDiff), nil
}

func comparePatch(baseSchemaDiff jd.Diff, fullSchemaDiff jd.Diff) string {
	normalizedBaseDiff := normalizeDiff(baseSchemaDiff)
	normalizedFullDiff := normalizeDiff(fullSchemaDiff)

	actualDiff := jd.Diff{}
	for _, fullDiffElement := range normalizedFullDiff {
		diffFoundInBase := false
		for _, baseDiffElement := range normalizedBaseDiff {
			// if the diff found in full schema diff is also available
			// in base schema diff, then ignore it
			if equalDiffElement(fullDiffElement, baseDiffElement) {
				diffFoundInBase = true
				break
			}
		}

		if !diffFoundInBase {
			actualDiff = append(actualDiff, fullDiffElement)
		}
	}

	return actualDiff.Render()
}

func normalizeDiff(diff jd.Diff) jd.Diff {
	output := jd.Diff{}
	for _, r := range diff {
		output = append(output, jd.DiffElement{
			Path:     normalizePath(r.Path),
			Metadata: r.Metadata,
			Before:   []jd.JsonNode{},
			Add:      r.Add,
			Remove:   r.Remove,
			After:    []jd.JsonNode{},
		})
	}

	return output
}

func normalizePath(path jd.Path) jd.Path {
	output := jd.Path{}
	for _, r := range path {
		if _, ok := r.(jd.PathIndex); ok {
			output = append(output, jd.PathIndex(0))
			continue
		}

		output = append(output, r)
	}

	return output
}

func equalDiffElement(a, b jd.DiffElement) bool {
	return reflect.DeepEqual(a, b)
}
