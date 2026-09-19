package core

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
)

// persistedRefWalker reports the declared references of one payload struct to
// a dagql.PersistedRefVisitor and records whether any reference was replaced.
// It is pure: it never loads rows, opens storage or constructs typed values.
type persistedRefWalker struct {
	visit   dagql.PersistedRefVisitor
	path    dagql.PersistedRefPath
	changed *bool
}

func newPersistedRefWalker(visit dagql.PersistedRefVisitor, path dagql.PersistedRefPath) *persistedRefWalker {
	return &persistedRefWalker{visit: visit, path: path, changed: new(bool)}
}

func (w *persistedRefWalker) at(field string) *persistedRefWalker {
	return &persistedRefWalker{visit: w.visit, path: w.path.Field(field), changed: w.changed}
}

func (w *persistedRefWalker) index(i int) *persistedRefWalker {
	return &persistedRefWalker{visit: w.visit, path: w.path.Index(i), changed: w.changed}
}

func (w *persistedRefWalker) note(changed bool) {
	if changed {
		*w.changed = true
	}
}

// child reports a required child row stored in a uint64 field. Zero is
// explicit absence and is not a reference.
func (w *persistedRefWalker) child(field string, id *uint64) error {
	changed, err := dagql.VisitPersistedRow(w.visit, dagql.PersistedRefChild, w.path.Field(field), id)
	w.note(changed)
	return err
}

// children reports an ordered list of required child rows.
func (w *persistedRefWalker) children(field string, ids []uint64) error {
	for i := range ids {
		changed, err := dagql.VisitPersistedRow(w.visit, dagql.PersistedRefChild, w.path.Field(field).Index(i), &ids[i])
		if err != nil {
			return err
		}
		w.note(changed)
	}
	return nil
}

// callID reports the row named by a handle-form typed call ID. Recipe-form
// IDs are typed call descriptions and pass through untouched.
func (w *persistedRefWalker) callID(field string, raw *string) error {
	changed, err := dagql.VisitPersistedCallID(w.visit, dagql.PersistedRefChild, w.path.Field(field), raw)
	w.note(changed)
	return err
}

// services reports every service row of an ordered binding list.
func (w *persistedRefWalker) services(field string, bindings []persistedServiceBinding) error {
	for i := range bindings {
		if err := w.at(field).index(i).child("serviceResultID", &bindings[i].ServiceResultID); err != nil {
			return err
		}
	}
	return nil
}

// roles reports the owner's declared storage roles with one classification.
func (w *persistedRefWalker) roles(kind dagql.PersistedRefKind, links []dagql.PersistedSnapshotRefLink) error {
	return dagql.VisitPersistedSnapshotRoles(w.visit, kind, w.path, links)
}

// lazy walks a nested lazy payload of a known kind through its visitor and
// writes the rewritten bytes back when a reference changed.
func (w *persistedRefWalker) lazy(field string, raw *json.RawMessage, visitor persistedLazyVisitor) error {
	if raw == nil || len(*raw) == 0 {
		return nil
	}
	sub := w.at(field)
	rewritten, err := visitor(*raw, sub)
	if err != nil {
		return err
	}
	if *sub.changed {
		*raw = rewritten
	}
	return nil
}

// visitPersistedPayload decodes one payload struct losslessly, walks its
// declared references and re-encodes it only when a reference was replaced.
func visitPersistedPayload[T any](v dagql.PersistedPayloadVisit, visit dagql.PersistedRefVisitor, walk func(*T, *persistedRefWalker) error) (json.RawMessage, error) {
	var payload T
	if len(v.Payload) > 0 {
		if err := unmarshalPersistedPayload(v.Payload, &payload); err != nil {
			return nil, fmt.Errorf("decode payload: %w", err)
		}
	}
	w := newPersistedRefWalker(visit, v.Path)
	if err := walk(&payload, w); err != nil {
		return nil, err
	}
	if !*w.changed {
		return v.Payload, nil
	}
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("re-encode payload: %w", err)
	}
	return rewritten, nil
}

// persistedPayloadVisitorFunc adapts a payload-struct walk into a family
// visitor. roles classifies the owner's storage roles; nil declares none.
type persistedPayloadVisitorFunc func(v dagql.PersistedPayloadVisit, visit dagql.PersistedRefVisitor) (json.RawMessage, error)

func (f persistedPayloadVisitorFunc) VisitPersistedReferences(v dagql.PersistedPayloadVisit, visit dagql.PersistedRefVisitor) (json.RawMessage, error) {
	return f(v, visit)
}

// persistedStructVisitor builds a family visitor for a payload struct whose
// references are walked by walk and whose storage roles, if any, all share
// one classification.
func persistedStructVisitor[T any](roles dagql.PersistedRefKind, walk func(*T, *persistedRefWalker) error) dagql.PersistedPayloadVisitor {
	return persistedPayloadVisitorFunc(func(v dagql.PersistedPayloadVisit, visit dagql.PersistedRefVisitor) (json.RawMessage, error) {
		if roles == "" && len(v.SnapshotLinks) > 0 {
			return nil, fmt.Errorf("payload declares no storage roles but has %d snapshot links", len(v.SnapshotLinks))
		}
		if roles != "" {
			if err := newPersistedRefWalker(visit, v.Path).roles(roles, v.SnapshotLinks); err != nil {
				return nil, err
			}
		}
		return visitPersistedPayload(v, visit, walk)
	})
}

// persistedLazyVisitor walks one lazy payload kind.
type persistedLazyVisitor func(raw json.RawMessage, w *persistedRefWalker) (json.RawMessage, error)

func persistedLazyStructVisitor[T any](walk func(*T, *persistedRefWalker) error) persistedLazyVisitor {
	return func(raw json.RawMessage, w *persistedRefWalker) (json.RawMessage, error) {
		var payload T
		if err := unmarshalPersistedPayload(raw, &payload); err != nil {
			return nil, fmt.Errorf("decode lazy payload: %w", err)
		}
		if err := walk(&payload, w); err != nil {
			return nil, err
		}
		if !*w.changed {
			return raw, nil
		}
		rewritten, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("re-encode lazy payload: %w", err)
		}
		return rewritten, nil
	}
}

// parentOnly walks lazy payloads whose only reference is their parent row.
func parentOnly[T any](parent func(*T) *uint64) persistedLazyVisitor {
	return persistedLazyStructVisitor(func(p *T, w *persistedRefWalker) error {
		return w.child("parentResultID", parent(p))
	})
}

// parentAndSource walks lazy payloads referencing their parent and one source
// row stored under the given field name.
func parentAndSource[T any](sourceField string, parent, source func(*T) *uint64) persistedLazyVisitor {
	return persistedLazyStructVisitor(func(p *T, w *persistedRefWalker) error {
		if err := w.child("parentResultID", parent(p)); err != nil {
			return err
		}
		return w.child(sourceField, source(p))
	})
}

// persistedDirectoryLazyVisitors declares the references of every Directory
// lazy kind, mirroring decodePersistedDirectoryLazy.
var persistedDirectoryLazyVisitors = map[string]persistedLazyVisitor{
	persistedDirectoryLazyKindGitCommitTree: persistedLazyStructVisitor(func(p *persistedDirectoryGitCommitTreeLazy, w *persistedRefWalker) error {
		if err := p.validate(); err != nil {
			return err
		}
		if err := w.child("commitResultID", &p.CommitResultID); err != nil {
			return err
		}
		return nil
	}),

	persistedDirectoryLazyKindGitTree: persistedLazyStructVisitor(func(p *persistedDirectoryGitTreeLazy, w *persistedRefWalker) error {
		if err := p.validate(); err != nil {
			return err
		}
		if err := w.child("refResultID", &p.RefResultID); err != nil {
			return err
		}
		return nil
	}),

	persistedDirectoryLazyKindGitBundleImport: persistedLazyStructVisitor(func(p *persistedDirectoryGitBundleImportLazy, w *persistedRefWalker) error {
		if err := p.validate(); err != nil {
			return err
		}
		if err := w.child("repoResultID", &p.RepoResultID); err != nil {
			return err
		}
		if err := w.child("bundleResultID", &p.BundleResultID); err != nil {
			return err
		}
		return nil
	}),

	persistedDirectoryLazyKindGitCleaned: persistedLazyStructVisitor(func(p *persistedDirectoryGitCleanedLazy, w *persistedRefWalker) error {
		if err := p.validate(); err != nil {
			return err
		}
		if err := w.child("repoResultID", &p.RepoResultID); err != nil {
			return err
		}
		return nil
	}),

	persistedDirectoryLazyKindContainerRootFS:    parentOnly(func(p *persistedContainerRootFSLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindContainerDirectory: parentOnly(func(p *persistedContainerDirectoryLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindWithDirectory: parentAndSource("sourceResultID",
		func(p *persistedDirectoryWithDirectoryLazy) *uint64 { return &p.ParentResultID },
		func(p *persistedDirectoryWithDirectoryLazy) *uint64 { return &p.SourceResultID }),
	persistedDirectoryLazyKindWithDirectoryDockerfileCompat: parentAndSource("sourceResultID",
		func(p *persistedDirectoryWithDirectoryDockerfileCompatLazy) *uint64 { return &p.ParentResultID },
		func(p *persistedDirectoryWithDirectoryDockerfileCompatLazy) *uint64 { return &p.SourceResultID }),
	persistedDirectoryLazyKindWithPatchFile: parentAndSource("patchResultID",
		func(p *persistedDirectoryWithPatchFileLazy) *uint64 { return &p.ParentResultID },
		func(p *persistedDirectoryWithPatchFileLazy) *uint64 { return &p.PatchResultID }),
	persistedDirectoryLazyKindWithNewFile: parentOnly(func(p *persistedDirectoryWithNewFileLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindWithFile: parentAndSource("sourceResultID",
		func(p *persistedDirectoryWithFileLazy) *uint64 { return &p.ParentResultID },
		func(p *persistedDirectoryWithFileLazy) *uint64 { return &p.SourceResultID }),
	persistedDirectoryLazyKindWithTimestamps:   parentOnly(func(p *persistedDirectoryWithTimestampsLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindWithNewDirectory: parentOnly(func(p *persistedDirectoryWithNewDirectoryLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindSubdirectory:     parentOnly(func(p *persistedDirectorySubdirectoryLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindDiff: parentAndSource("otherResultID",
		func(p *persistedDirectoryDiffLazy) *uint64 { return &p.ParentResultID },
		func(p *persistedDirectoryDiffLazy) *uint64 { return &p.OtherResultID }),
	persistedDirectoryLazyKindWithChanges: parentAndSource("changesResultID",
		func(p *persistedDirectoryWithChangesLazy) *uint64 { return &p.ParentResultID },
		func(p *persistedDirectoryWithChangesLazy) *uint64 { return &p.ChangesResultID }),
	persistedDirectoryLazyKindWithout:     parentOnly(func(p *persistedDirectoryWithoutLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindWithSymlink: parentOnly(func(p *persistedDirectoryWithSymlinkLazy) *uint64 { return &p.ParentResultID }),
	persistedDirectoryLazyKindChown:       parentOnly(func(p *persistedDirectoryChownLazy) *uint64 { return &p.ParentResultID }),
}

// persistedFileLazyVisitors declares the references of every File lazy kind,
// mirroring decodePersistedFileLazy.
var persistedFileLazyVisitors = map[string]persistedLazyVisitor{
	persistedFileLazyKindHTTPResolve: persistedLazyStructVisitor(func(p *persistedFileHTTPResolveLazy, _ *persistedRefWalker) error { return p.validate() }),

	persistedFileLazyKindBlob:           persistedLazyStructVisitor(func(*persistedFileBlobLazy, *persistedRefWalker) error { return nil }),
	persistedFileLazyKindDirectoryFile:  parentOnly(func(p *persistedFileSubfileLazy) *uint64 { return &p.ParentResultID }),
	persistedFileLazyKindContainerFile:  parentOnly(func(p *persistedContainerFileLazy) *uint64 { return &p.ParentResultID }),
	persistedFileLazyKindWithName:       parentOnly(func(p *persistedFileWithNameLazy) *uint64 { return &p.ParentResultID }),
	persistedFileLazyKindWithReplaced:   parentOnly(func(p *persistedFileWithReplacedLazy) *uint64 { return &p.ParentResultID }),
	persistedFileLazyKindWithTimestamps: parentOnly(func(p *persistedFileWithTimestampsLazy) *uint64 { return &p.ParentResultID }),
	persistedFileLazyKindChown:          parentOnly(func(p *persistedFileChownLazy) *uint64 { return &p.ParentResultID }),
}

// persistedContainerRecipeVisitors declares the references of every Container
// recipe payload keyed by the recorded call field, mirroring
// decodePersistedContainerRecipe.
var persistedContainerRecipeVisitors = map[string]persistedLazyVisitor{
	"_builtinContainer": persistedLazyStructVisitor(func(p *persistedContainerBuiltinLazy, _ *persistedRefWalker) error { return p.validate() }),

	"withEntrypoint":            parentOnly(func(p *persistedContainerWithEntrypointLazy) *uint64 { return &p.ParentResultID }),
	"withoutEntrypoint":         parentOnly(func(p *persistedContainerWithoutEntrypointLazy) *uint64 { return &p.ParentResultID }),
	"withDefaultArgs":           parentOnly(func(p *persistedContainerWithDefaultArgsLazy) *uint64 { return &p.ParentResultID }),
	"withoutDefaultArgs":        parentOnly(func(p *persistedContainerWithoutDefaultArgsLazy) *uint64 { return &p.ParentResultID }),
	"withUser":                  parentOnly(func(p *persistedContainerWithUserLazy) *uint64 { return &p.ParentResultID }),
	"withoutUser":               parentOnly(func(p *persistedContainerWithoutUserLazy) *uint64 { return &p.ParentResultID }),
	"withWorkdir":               parentOnly(func(p *persistedContainerWithWorkdirLazy) *uint64 { return &p.ParentResultID }),
	"withoutWorkdir":            parentOnly(func(p *persistedContainerWithoutWorkdirLazy) *uint64 { return &p.ParentResultID }),
	"withEnvVariable":           parentOnly(func(p *persistedContainerWithEnvVariableLazy) *uint64 { return &p.ParentResultID }),
	"withEnvFileVariables":      parentAndSource("sourceResultID", func(p *persistedContainerWithEnvFileVariablesLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithEnvFileVariablesLazy) *uint64 { return &p.SourceResultID }),
	"__withSystemEnvVariable":   parentOnly(func(p *persistedContainerWithSystemEnvVariableLazy) *uint64 { return &p.ParentResultID }),
	"withVolatileVariable":      parentOnly(func(p *persistedContainerWithVolatileVariableLazy) *uint64 { return &p.ParentResultID }),
	"withoutEnvVariable":        parentOnly(func(p *persistedContainerWithoutEnvVariableLazy) *uint64 { return &p.ParentResultID }),
	"withoutVolatileVariable":   parentOnly(func(p *persistedContainerWithoutVolatileVariableLazy) *uint64 { return &p.ParentResultID }),
	"withLabel":                 parentOnly(func(p *persistedContainerWithLabelLazy) *uint64 { return &p.ParentResultID }),
	"withoutLabel":              parentOnly(func(p *persistedContainerWithoutLabelLazy) *uint64 { return &p.ParentResultID }),
	"__withImageConfigMetadata": parentOnly(func(p *persistedContainerWithImageConfigMetadataLazy) *uint64 { return &p.ParentResultID }),
	"withDockerHealthcheck":     parentOnly(func(p *persistedContainerWithHealthcheckLazy) *uint64 { return &p.ParentResultID }),
	"withoutDockerHealthcheck":  parentOnly(func(p *persistedContainerWithoutHealthcheckLazy) *uint64 { return &p.ParentResultID }),
	"experimentalWithGPU":       parentOnly(func(p *persistedContainerSetGPUsLazy) *uint64 { return &p.ParentResultID }),
	"experimentalWithAllGPUs":   parentOnly(func(p *persistedContainerSetGPUsLazy) *uint64 { return &p.ParentResultID }),
	"withAnnotation":            parentOnly(func(p *persistedContainerWithAnnotationLazy) *uint64 { return &p.ParentResultID }),
	"withoutAnnotation":         parentOnly(func(p *persistedContainerWithoutAnnotationLazy) *uint64 { return &p.ParentResultID }),
	"withSecretVariable":        parentAndSource("secretResultID", func(p *persistedContainerWithSecretVariableLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithSecretVariableLazy) *uint64 { return &p.SecretResultID }),
	"withoutSecretVariable":     parentOnly(func(p *persistedContainerWithoutSecretVariableLazy) *uint64 { return &p.ParentResultID }),
	"withServiceBinding":        parentAndSource("serviceResultID", func(p *persistedContainerWithServiceBindingLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithServiceBindingLazy) *uint64 { return &p.ServiceResultID }),
	"withExposedPort":           parentOnly(func(p *persistedContainerWithExposedPortLazy) *uint64 { return &p.ParentResultID }),
	"withoutExposedPort":        parentOnly(func(p *persistedContainerWithoutExposedPortLazy) *uint64 { return &p.ParentResultID }),
	"withDefaultTerminalCmd":    parentOnly(func(p *persistedContainerWithDefaultTerminalCmdLazy) *uint64 { return &p.ParentResultID }),
	"from": persistedLazyStructVisitor(func(p *persistedContainerFromLazy, w *persistedRefWalker) error {
		if err := w.child("parentResultID", &p.ParentResultID); err != nil {
			return err
		}
		return w.services("registryServices", p.RegistryServices)
	}),
	"withRootfs":                        parentAndSource("sourceResultID", func(p *persistedContainerWithRootFSLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithRootFSLazy) *uint64 { return &p.SourceResultID }),
	"withDirectory":                     parentAndSource("sourceResultID", func(p *persistedContainerWithDirectoryLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithDirectoryLazy) *uint64 { return &p.SourceResultID }),
	"withFile":                          parentAndSource("sourceResultID", func(p *persistedContainerWithFileLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithFileLazy) *uint64 { return &p.SourceResultID }),
	"withNewFile":                       parentAndSource("sourceResultID", func(p *persistedContainerWithFileLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithFileLazy) *uint64 { return &p.SourceResultID }),
	"withMountedDirectory":              parentAndSource("sourceResultID", func(p *persistedContainerWithMountedDirectoryLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithMountedDirectoryLazy) *uint64 { return &p.SourceResultID }),
	"withMountedFile":                   parentAndSource("sourceResultID", func(p *persistedContainerWithMountedFileLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithMountedFileLazy) *uint64 { return &p.SourceResultID }),
	"__withMountedPathDockerfileCompat": parentAndSource("sourceResultID", func(p *persistedContainerWithMountedPathDockerfileCompatLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithMountedPathDockerfileCompatLazy) *uint64 { return &p.SourceResultID }),
	"withMountedCache":                  parentAndSource("cacheResultID", func(p *persistedContainerWithMountedCacheLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithMountedCacheLazy) *uint64 { return &p.CacheResultID }),
	"withMountedVolume":                 parentAndSource("volumeResultID", func(p *persistedContainerWithMountedVolumeLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithMountedVolumeLazy) *uint64 { return &p.VolumeResultID }),
	"withMountedTemp":                   parentOnly(func(p *persistedContainerWithMountedTempLazy) *uint64 { return &p.ParentResultID }),
	"withMountedSecret":                 parentAndSource("sourceResultID", func(p *persistedContainerWithMountedSecretLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithMountedSecretLazy) *uint64 { return &p.SourceResultID }),
	"withoutMount":                      parentOnly(func(p *persistedContainerWithoutMountLazy) *uint64 { return &p.ParentResultID }),
	"withoutDirectory":                  parentOnly(func(p *persistedContainerWithoutPathLazy) *uint64 { return &p.ParentResultID }),
	"withoutFile":                       parentOnly(func(p *persistedContainerWithoutPathLazy) *uint64 { return &p.ParentResultID }),
	"withoutFiles":                      parentOnly(func(p *persistedContainerWithoutPathLazy) *uint64 { return &p.ParentResultID }),
	"withSymlink":                       parentOnly(func(p *persistedContainerWithSymlinkLazy) *uint64 { return &p.ParentResultID }),
	"withUnixSocket":                    parentAndSource("sourceResultID", func(p *persistedContainerWithUnixSocketLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerWithUnixSocketLazy) *uint64 { return &p.SourceResultID }),
	"withoutUnixSocket":                 parentOnly(func(p *persistedContainerWithoutUnixSocketLazy) *uint64 { return &p.ParentResultID }),
	"import":                            parentAndSource("sourceResultID", func(p *persistedContainerImportLazy) *uint64 { return &p.ParentResultID }, func(p *persistedContainerImportLazy) *uint64 { return &p.SourceResultID }),
	"withExec": persistedLazyStructVisitor(func(p *persistedContainerExecLazy, w *persistedRefWalker) error {
		if err := w.child("parentResultID", &p.ParentResultID); err != nil {
			return err
		}
		if err := w.child("moduleContextResultID", &p.ModuleContextResultID); err != nil {
			return err
		}
		return w.child("volatileCacheHitParentResultID", &p.VolatileCacheHitParentResultID)
	}),
}

func visitPersistedLazyKind(w *persistedRefWalker, field string, kind string, raw *json.RawMessage, visitors map[string]persistedLazyVisitor, what string) error {
	if raw == nil || len(*raw) == 0 {
		if kind == "" {
			return nil
		}
		return fmt.Errorf("%s lazy kind %q has no payload", what, kind)
	}
	visitor, ok := visitors[kind]
	if !ok {
		return fmt.Errorf("%s lazy kind %q has no reference visitor", what, kind)
	}
	return w.lazy(field, raw, visitor)
}

var persistedDirectoryVisitor = persistedStructVisitor(dagql.PersistedRefOutputRole, func(p *persistedDirectoryPayload, w *persistedRefWalker) error {
	if err := w.services("services", p.Services); err != nil {
		return err
	}
	return visitPersistedLazyKind(w, "lazyJSON", p.LazyKind, &p.LazyJSON, persistedDirectoryLazyVisitors, "directory")
})

var persistedFileVisitor = persistedStructVisitor(dagql.PersistedRefOutputRole, func(p *persistedFilePayload, w *persistedRefWalker) error {
	if err := w.services("services", p.Services); err != nil {
		return err
	}
	return visitPersistedLazyKind(w, "lazyJSON", p.LazyKind, &p.LazyJSON, persistedFileLazyVisitors, "file")
})

// persistedContainerVisitor walks metadata references, per-part service
// bindings and producer payloads selected by the recorded call's field,
// which pending container decode also dispatches on.
var persistedContainerVisitor = persistedPayloadVisitorFunc(func(v dagql.PersistedPayloadVisit, visit dagql.PersistedRefVisitor) (json.RawMessage, error) {
	if err := newPersistedRefWalker(visit, v.Path).roles(dagql.PersistedRefOutputRole, v.SnapshotLinks); err != nil {
		return nil, err
	}
	return visitPersistedPayload(v, visit, func(p *persistedContainerPayload, w *persistedRefWalker) error {
		meta := w.at("metadata").at("value")
		for i := range p.Metadata.Value.Mounts {
			mount := meta.at("mounts").index(i)
			if err := mount.child("cacheSourceResultID", &p.Metadata.Value.Mounts[i].CacheSourceResultID); err != nil {
				return err
			}
			if err := mount.child("volumeSourceResultID", &p.Metadata.Value.Mounts[i].VolumeSourceResultID); err != nil {
				return err
			}
		}
		for i := range p.Metadata.Value.Secrets {
			if err := meta.at("secrets").index(i).child("secretResultID", &p.Metadata.Value.Secrets[i].SecretResultID); err != nil {
				return err
			}
		}
		for i := range p.Metadata.Value.Sockets {
			if err := meta.at("sockets").index(i).child("sourceResultID", &p.Metadata.Value.Sockets[i].SourceResultID); err != nil {
				return err
			}
		}
		if err := meta.services("services", p.Metadata.Value.Services); err != nil {
			return err
		}
		for _, part := range slices.Sorted(maps.Keys(p.Parts)) {
			record := p.Parts[part]
			if err := w.at("parts").at(string(part)).services("services", record.Services); err != nil {
				return err
			}
			p.Parts[part] = record
		}
		if len(p.LazyJSON) == 0 {
			return nil
		}
		if v.Call == nil {
			return fmt.Errorf("pending container recipe has no recorded call")
		}
		return visitPersistedLazyKind(w, "lazyJSON", v.Call.Field, &p.LazyJSON, persistedContainerRecipeVisitors, "container")
	})
})

var persistedServiceVisitor = persistedStructVisitor("", func(p *persistedServicePayload, w *persistedRefWalker) error {
	if err := w.child("containerResultID", &p.ContainerResultID); err != nil {
		return err
	}
	if err := w.child("moduleContextResultID", &p.ModuleContextResultID); err != nil {
		return err
	}
	return w.child("tunnelUpstreamResultID", &p.TunnelUpstreamResultID)
})

var persistedVolumeVisitor = persistedStructVisitor("", func(p *persistedVolumePayload, w *persistedRefWalker) error {
	if p.SSHFS == nil {
		return nil
	}
	sshfs := w.at("sshfs")
	if err := sshfs.child("privateKeyResultID", &p.SSHFS.PrivateKeyResultID); err != nil {
		return err
	}
	if err := sshfs.child("knownHostsResultID", &p.SSHFS.KnownHostsResultID); err != nil {
		return err
	}
	return sshfs.child("serviceHostResultID", &p.SSHFS.ServiceHostResultID)
})

// persistedCacheVolumeVisitor: the optional source row is an ordinary child;
// the mutable snapshot is local backing configuration.
var persistedCacheVolumeVisitor = persistedStructVisitor(dagql.PersistedRefLocalBacking, func(p *persistedCacheVolumePayload, w *persistedRefWalker) error {
	return w.child("sourceResultID", &p.SourceResultID)
})

var persistedClientFilesyncMirrorVisitor = persistedStructVisitor(dagql.PersistedRefLocalBacking, func(*persistedClientFilesyncMirrorPayload, *persistedRefWalker) error { return nil })

var persistedRemoteGitMirrorVisitor = persistedStructVisitor(dagql.PersistedRefLocalBacking, func(*persistedRemoteGitMirrorPayload, *persistedRefWalker) error { return nil })

var persistedHTTPStateVisitor = persistedStructVisitor(dagql.PersistedRefOutputRole, func(*persistedHTTPStatePayload, *persistedRefWalker) error { return nil })

var persistedGitRepositoryVisitor = persistedStructVisitor("", func(p *persistedGitRepositoryPayload, w *persistedRefWalker) error {
	if p.Local != nil {
		if err := w.at("local").child("directoryResultID", &p.Local.DirectoryResultID); err != nil {
			return err
		}
	}
	if p.Remote != nil {
		return visitPersistedRemoteGitRepositoryRefs(w.at("remote"), p.Remote)
	}
	return nil
})

var persistedGitRefVisitor = persistedStructVisitor("", func(p *persistedGitRefPayload, w *persistedRefWalker) error {
	return w.child("repoResultID", &p.RepoResultID)
})

var persistedGitCommitVisitor = persistedStructVisitor("", func(p *persistedGitCommitPayload, w *persistedRefWalker) error {
	return w.child("repoResultID", &p.RepoResultID)
})

var persistedGitBundleVisitor = persistedStructVisitor("", func(p *persistedGitBundlePayload, w *persistedRefWalker) error {
	return w.child("fileResultID", &p.FileResultID)
})

var persistedChangesetVisitor = persistedStructVisitor("", func(p *persistedChangesetPayload, w *persistedRefWalker) error {
	if err := w.child("beforeResultID", &p.BeforeResultID); err != nil {
		return err
	}
	return w.child("afterResultID", &p.AfterResultID)
})

var persistedGeneratedCodeVisitor = persistedStructVisitor("", func(p *persistedGeneratedCodePayload, w *persistedRefWalker) error {
	return w.child("codeResultID", &p.CodeResultID)
})

var persistedModuleVisitor = persistedStructVisitor("", func(p *persistedModulePayload, w *persistedRefWalker) error {
	if err := w.child("sourceResultID", &p.SourceResultID); err != nil {
		return err
	}
	if err := w.child("contextSourceResultID", &p.ContextSourceResultID); err != nil {
		return err
	}
	if err := w.child("runtimeResultID", &p.RuntimeResultID); err != nil {
		return err
	}
	if err := w.children("depModuleResultIDs", p.DepModuleResultIDs); err != nil {
		return err
	}
	if err := w.children("objectDefResultIDs", p.ObjectDefResultIDs); err != nil {
		return err
	}
	if err := w.children("interfaceDefResultIDs", p.InterfaceDefResultIDs); err != nil {
		return err
	}
	return w.children("enumDefResultIDs", p.EnumDefResultIDs)
})

var persistedModuleSourceVisitor = persistedStructVisitor("", func(p *persistedModuleSourcePayload, w *persistedRefWalker) error {
	if err := w.children("dependencyResultIDs", p.DependencyResultIDs); err != nil {
		return err
	}
	if err := w.child("blueprintResultID", &p.BlueprintResultID); err != nil {
		return err
	}
	if err := w.children("toolchainResultIDs", p.ToolchainResultIDs); err != nil {
		return err
	}
	if err := w.child("contextDirectoryResultID", &p.ContextDirectoryResultID); err != nil {
		return err
	}
	if err := w.child("workspaceResultID", &p.WorkspaceResultID); err != nil {
		return err
	}
	if err := w.child("gitUnfilteredContextDirResultID", &p.GitUnfilteredContextDirResultID); err != nil {
		return err
	}
	if p.DirSrc != nil {
		return w.at("dirSrc").child("originalContextDirResultID", &p.DirSrc.OriginalContextDirResultID)
	}
	return nil
})

func visitPersistedWorkspaceSourceRefs(w *persistedRefWalker, src *persistedWorkspaceSource) error {
	if src == nil {
		return nil
	}
	if err := w.child("rootResultID", &src.RootResultID); err != nil {
		return err
	}
	if err := w.child("gitRefResultID", &src.GitRefResultID); err != nil {
		return err
	}
	if err := w.child("changesID", &src.ChangesID); err != nil {
		return err
	}
	return visitPersistedWorkspaceSourceRefs(w.at("base"), src.Base)
}

var persistedWorkspaceVisitor = persistedStructVisitor("", func(p *persistedWorkspacePayload, w *persistedRefWalker) error {
	if err := w.child("rootfsResultID", &p.RootfsResultID); err != nil {
		return err
	}
	if err := w.child("mountsResultID", &p.MountsResultID); err != nil {
		return err
	}
	return visitPersistedWorkspaceSourceRefs(w.at("source"), p.Source)
})

var persistedWorkspaceGitVisitor = persistedStructVisitor("", func(p *persistedWorkspaceGitPayload, w *persistedRefWalker) error {
	return w.child("workspaceResultID", &p.WorkspaceResultID)
})

// visitPersistedModTreeRefs walks the module and type rows of a node table.
// Node IDs and parent IDs index the table itself and are not rows.
func visitPersistedModTreeRefs(w *persistedRefWalker, tree *persistedModTree) error {
	for i := range tree.Nodes {
		node := w.at("nodes").index(i)
		if err := node.child("moduleResultID", &tree.Nodes[i].ModuleResultID); err != nil {
			return err
		}
		if err := node.child("originalModuleResultID", &tree.Nodes[i].OriginalModuleResultID); err != nil {
			return err
		}
		if err := node.child("typeResultID", &tree.Nodes[i].TypeResultID); err != nil {
			return err
		}
	}
	return nil
}

func visitPersistedGeneratorRefs(w *persistedRefWalker, g *persistedGeneratorPayload) error {
	if err := w.child("changesResultID", &g.ChangesResultID); err != nil {
		return err
	}
	if err := w.child("workspaceBaseResultID", &g.WorkspaceBaseResultID); err != nil {
		return err
	}
	return w.child("workspaceResultID", &g.WorkspaceResultID)
}

var persistedGeneratorVisitor = persistedStructVisitor("", func(p *persistedGeneratorObjectPayload, w *persistedRefWalker) error {
	if err := visitPersistedModTreeRefs(w.at("tree"), &p.Tree); err != nil {
		return err
	}
	return visitPersistedGeneratorRefs(w.at("generator"), &p.Generator)
})

var persistedGeneratorGroupVisitor = persistedStructVisitor("", func(p *persistedGeneratorGroupPayload, w *persistedRefWalker) error {
	if err := visitPersistedModTreeRefs(w.at("tree"), &p.Tree); err != nil {
		return err
	}
	for i := range p.Generators {
		if err := visitPersistedGeneratorRefs(w.at("generators").index(i), &p.Generators[i]); err != nil {
			return err
		}
	}
	return w.child("boundWorkspaceResultID", &p.BoundWorkspaceResultID)
})

// visitPersistedModuleObjectValue walks the tagged field forms of a module
// object: attached result references and handle-form typed IDs are rows;
// scalars, nulls and nested arrays/objects are data.
func visitPersistedModuleObjectValue(w *persistedRefWalker, val *persistedModuleObjectValue) error {
	switch val.Kind {
	case persistedModuleObjectValueKindResultRef:
		return w.child("resultID", &val.ResultID)
	case persistedModuleObjectValueKindCallID:
		return w.callID("callID", &val.CallID)
	case persistedModuleObjectValueKindArray:
		for i := range val.Items {
			if err := visitPersistedModuleObjectValue(w.at("items").index(i), &val.Items[i]); err != nil {
				return err
			}
		}
	case persistedModuleObjectValueKindObject:
		for _, name := range slices.Sorted(maps.Keys(val.Fields)) {
			field := val.Fields[name]
			if err := visitPersistedModuleObjectValue(w.at("fields").at(name), &field); err != nil {
				return err
			}
			val.Fields[name] = field
		}
	}
	return nil
}

var persistedModuleObjectVisitor = persistedStructVisitor("", func(p *persistedModuleObjectPayload, w *persistedRefWalker) error {
	for _, name := range slices.Sorted(maps.Keys(p.Fields)) {
		field := p.Fields[name]
		if err := visitPersistedModuleObjectValue(w.at("fields").at(name), &field); err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
		p.Fields[name] = field
	}
	return nil
})

var persistedFunctionVisitor = persistedStructVisitor("", func(p *persistedFunction, w *persistedRefWalker) error {
	if err := w.children("argResultIDs", p.ArgResultIDs); err != nil {
		return err
	}
	if err := w.child("returnTypeResultID", &p.ReturnTypeResultID); err != nil {
		return err
	}
	return w.child("sourceMapResultID", &p.SourceMapResultID)
})

var persistedFunctionArgVisitor = persistedStructVisitor("", func(p *persistedFunctionArg, w *persistedRefWalker) error {
	if err := w.child("sourceMapResultID", &p.SourceMapResultID); err != nil {
		return err
	}
	return w.child("typeDefResultID", &p.TypeDefResultID)
})

var persistedTypeDefVisitor = persistedStructVisitor("", func(p *persistedTypeDef, w *persistedRefWalker) error {
	for _, ref := range []struct {
		field string
		id    *uint64
	}{
		{"asListResultID", &p.AsListResultID},
		{"asObjectResultID", &p.AsObjectResultID},
		{"asInterfaceResultID", &p.AsInterfaceResultID},
		{"asInputResultID", &p.AsInputResultID},
		{"asScalarResultID", &p.AsScalarResultID},
		{"asEnumResultID", &p.AsEnumResultID},
	} {
		if err := w.child(ref.field, ref.id); err != nil {
			return err
		}
	}
	return nil
})

var persistedObjectTypeDefVisitor = persistedStructVisitor("", func(p *persistedObjectTypeDef, w *persistedRefWalker) error {
	if err := w.child("sourceMapResultID", &p.SourceMapResultID); err != nil {
		return err
	}
	if err := w.children("fieldResultIDs", p.FieldResultIDs); err != nil {
		return err
	}
	if err := w.children("functionResultIDs", p.FunctionResultIDs); err != nil {
		return err
	}
	return w.child("constructorResultID", &p.ConstructorResultID)
})

var persistedFieldTypeDefVisitor = persistedStructVisitor("", func(p *persistedFieldTypeDef, w *persistedRefWalker) error {
	if err := w.child("typeDefResultID", &p.TypeDefResultID); err != nil {
		return err
	}
	return w.child("sourceMapResultID", &p.SourceMapResultID)
})

var persistedInterfaceTypeDefVisitor = persistedStructVisitor("", func(p *persistedInterfaceTypeDef, w *persistedRefWalker) error {
	if err := w.child("sourceMapResultID", &p.SourceMapResultID); err != nil {
		return err
	}
	return w.children("functionResultIDs", p.FunctionResultIDs)
})

var persistedListTypeDefVisitor = persistedStructVisitor("", func(p *persistedListTypeDef, w *persistedRefWalker) error {
	return w.child("elementTypeDefResultID", &p.ElementTypeDefResultID)
})

var persistedInputTypeDefVisitor = persistedStructVisitor("", func(p *persistedInputTypeDef, w *persistedRefWalker) error {
	return w.children("fieldResultIDs", p.FieldResultIDs)
})

var persistedEnumTypeDefVisitor = persistedStructVisitor("", func(p *persistedEnumTypeDef, w *persistedRefWalker) error {
	if err := w.children("memberResultIDs", p.MemberResultIDs); err != nil {
		return err
	}
	return w.child("sourceMapResultID", &p.SourceMapResultID)
})

var persistedEnumMemberTypeDefVisitor = persistedStructVisitor("", func(p *persistedEnumMemberTypeDef, w *persistedRefWalker) error {
	return w.child("sourceMapResultID", &p.SourceMapResultID)
})
