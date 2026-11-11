package core

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// persistedVisitorPayloadTypes is the payload struct each persisted family's
// visitor walks. A family whose visitor walks references must be here or in
// persistedVisitorCoverageExempt, so a new family is covered when it is added.
var persistedVisitorPayloadTypes = map[string]any{
	"core.Check":                  persistedCheck{},
	"core.Container":              persistedContainerPayload{},
	"core.Directory":              persistedDirectoryPayload{},
	"core.File":                   persistedFilePayload{},
	"core.CacheVolume":            persistedCacheVolumePayload{},
	"core.ClientFilesyncMirror":   persistedClientFilesyncMirrorPayload{},
	"core.RemoteGitMirror":        persistedRemoteGitMirrorPayload{},
	"core.HTTPState":              persistedHTTPStatePayload{},
	"core.Service":                persistedServicePayload{},
	"core.Volume":                 persistedVolumePayload{},
	"core.GitRepository":          persistedGitRepositoryPayload{},
	"core.GitRef":                 persistedGitRefPayload{},
	"core.GitRemoteHandle":        persistedGitRemoteHandle{},
	"core.GitCommit":              persistedGitCommitPayload{},
	"core.GitBundle":              persistedGitBundlePayload{},
	"core.Changeset":              persistedChangesetPayload{},
	"core.GeneratedCode":          persistedGeneratedCodePayload{},
	"core.Module":                 persistedModulePayload{},
	"core.ModuleSource":           persistedModuleSourcePayload{},
	"core.Workspace":              persistedWorkspacePayload{},
	"core.WorkspaceGit":           persistedWorkspaceGitPayload{},
	"core.Artifact":               persistedArtifacts{},
	"core.Artifacts":              persistedArtifacts{},
	"core.Expertise":              persistedArtifacts{},
	"core.Address":                persistedAddressPayload{},
	"core.WorkspaceSDK":           persistedWorkspaceSDK{},
	"core.ModuleObject":           persistedModuleObjectPayload{},
	"core.Function":               persistedFunction{},
	"core.FunctionArg":            persistedFunctionArg{},
	"core.TypeDef":                persistedTypeDef{},
	"core.ObjectTypeDef":          persistedObjectTypeDef{},
	"core.FieldTypeDef":           persistedFieldTypeDef{},
	"core.InterfaceTypeDef":       persistedInterfaceTypeDef{},
	"core.ListTypeDef":            persistedListTypeDef{},
	"core.InputTypeDef":           persistedInputTypeDef{},
	"core.EnumTypeDef":            persistedEnumTypeDef{},
	"core.EnumMemberTypeDef":      persistedEnumMemberTypeDef{},
	"core.CurrentModule":          persistedCurrentModulePayload{},
	"core.WorkspaceMigration":     persistedWorkspaceMigrationPayload{},
	"core.WorkspaceMigrationStep": persistedWorkspaceMigrationStepPayload{},
}

// persistedVisitorCoverageExempt are the families with references that the
// coverage test can't fill generically, each with why.
var persistedVisitorCoverageExempt = map[string]string{
	"core.CollectionTypeDef": "its whole payload is the one reference, a bare number, which its visitor walks",
}

// persistedVisitorDataFields are the uint64 payload fields, as
// "payload type.json name", that hold data rather than a row reference. Every
// other uint64 or []uint64 field the coverage test reaches is taken for a
// stored reference, whatever its name, so a new reference is checked without
// being listed.
var persistedVisitorDataFields = map[string]string{}

// TestPersistedVisitorsWalkEveryStoredReference sets every stored row
// reference in each family's payload to its own number and requires the
// family's visitor to report each one: a reference the visitor skips is
// neither relocated nor a dependency, and keeps the sending engine's number on
// the receiving one.
func TestPersistedVisitorsWalkEveryStoredReference(t *testing.T) {
	t.Parallel()
	families := map[string]dagql.PersistedObjectFamily{}
	for _, family := range append(slices.Clone(persistedObjectFamilies), persistedMetadataFamilies...) {
		families[family.Name] = family
	}
	for name, family := range families {
		if _, ok := family.Visitor.(dagql.PersistedNoReferences); ok {
			continue
		}
		_, typed := persistedVisitorPayloadTypes[name]
		_, exempt := persistedVisitorCoverageExempt[name]
		require.True(t, typed || exempt, "family %s walks references: give its payload type to persistedVisitorPayloadTypes, or say in persistedVisitorCoverageExempt why it can't be filled", name)
	}
	for name, sample := range persistedVisitorPayloadTypes {
		t.Run(name, func(t *testing.T) {
			family, ok := families[name]
			require.True(t, ok, "no persisted family %s", name)
			payload := reflect.New(reflect.TypeOf(sample)).Elem()
			var next uint64 = 1000
			stored := map[uint64]string{}
			fillPersistedReferences(payload, "", &next, stored, nil)
			raw, err := json.Marshal(payload.Interface())
			require.NoError(t, err)
			visited := map[uint64]bool{}
			_, err = family.Visitor.VisitPersistedReferences(dagql.PersistedPayloadVisit{Payload: raw}, func(ref *dagql.PersistedRef) error {
				visited[ref.ResultID] = true
				return nil
			})
			require.NoError(t, err)
			var missed []string
			for id, path := range stored {
				if !visited[id] {
					missed = append(missed, path)
				}
			}
			slices.Sort(missed)
			require.Empty(t, missed, "%s stores these references, which its visitor doesn't walk", name)
		})
	}
}

// fillPersistedReferences sets every reference field reachable in v to a
// distinct number, recording its path, and gives every nested struct, pointer,
// slice and map one element so their references are reached. A struct type
// already on the path isn't entered again: a recursive payload, such as a type
// definition holding type definitions, is filled one level deep.
func fillPersistedReferences(v reflect.Value, path string, next *uint64, stored map[uint64]string, onPath []reflect.Type) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.Type().Elem().Kind() != reflect.Struct {
			return
		}
		if slices.Contains(onPath, v.Type().Elem()) {
			return
		}
		v.Set(reflect.New(v.Type().Elem()))
		fillPersistedReferences(v.Elem(), path, next, stored, onPath)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(Platform{}) {
			// A platform must name one to encode.
			v.Set(reflect.ValueOf(Platform{OS: "linux", Architecture: "amd64"}))
			return
		}
		if v.Type() == reflect.TypeOf(persistedModuleObjectValue{}) {
			// A module object value's kind says what its other fields hold:
			// fill it as an attached result reference.
			*next++
			v.Set(reflect.ValueOf(persistedModuleObjectValue{Kind: persistedModuleObjectValueKindResultRef, ResultID: *next}))
			stored[*next] = path + ".resultID"
			return
		}
		if slices.Contains(onPath, v.Type()) {
			return
		}
		onPath = append(slices.Clip(onPath), v.Type())
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fv := v.Field(i)
			fpath := path + "." + name
			if _, data := persistedVisitorDataFields[v.Type().Name()+"."+name]; data {
				continue
			}
			switch {
			case fv.Kind() == reflect.Uint64:
				*next++
				fv.SetUint(*next)
				stored[*next] = fpath
			case fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Uint64:
				ids := reflect.MakeSlice(fv.Type(), 2, 2)
				for j := range 2 {
					*next++
					ids.Index(j).SetUint(*next)
					stored[*next] = fpath + "[" + string(rune('0'+j)) + "]"
				}
				fv.Set(ids)
			default:
				fillPersistedReferences(fv, fpath, next, stored, onPath)
			}
		}
	case reflect.Slice:
		elem := v.Type().Elem()
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() != reflect.Struct || slices.Contains(onPath, elem) {
			return
		}
		items := reflect.MakeSlice(v.Type(), 1, 1)
		fillPersistedReferences(items.Index(0), path+"[0]", next, stored, onPath)
		v.Set(items)
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		elem := reflect.New(v.Type().Elem()).Elem()
		fillPersistedReferences(elem, path+"[k]", next, stored, onPath)
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), elem)
		v.Set(m)
	}
}
