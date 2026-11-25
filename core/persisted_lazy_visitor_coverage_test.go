package core

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// persistedLazyPayloadTypes is the payload struct each lazy operation's
// visitor decodes, by the visitors' table and kind. A directory, file or
// container payload holds its lazy operation as JSON (lazyJSON), which
// TestPersistedVisitorsWalkEveryStoredReference leaves empty: this table is
// how the references inside it are checked. Every kind of each table must be
// here or in persistedLazyCoverageExempt, so a new kind is covered when it is
// added.
var persistedLazyPayloadTypes = map[string]map[string]any{
	"directory": {
		persistedDirectoryLazyKindGitCommitTree:                 persistedDirectoryGitCommitTreeLazy{},
		persistedDirectoryLazyKindGitTree:                       persistedDirectoryGitTreeLazy{},
		persistedDirectoryLazyKindGitFullCheckout:               persistedDirectoryGitTreeLazy{},
		persistedDirectoryLazyKindGitBundleImport:               persistedDirectoryGitBundleImportLazy{},
		persistedDirectoryLazyKindGitCleaned:                    persistedDirectoryGitCleanedLazy{},
		persistedDirectoryLazyKindContainerRootFS:               persistedContainerRootFSLazy{},
		persistedDirectoryLazyKindContainerDirectory:            persistedContainerDirectoryLazy{},
		persistedDirectoryLazyKindWithDirectory:                 persistedDirectoryWithDirectoryLazy{},
		persistedDirectoryLazyKindWithDirectoryDockerfileCompat: persistedDirectoryWithDirectoryDockerfileCompatLazy{},
		persistedDirectoryLazyKindWithPatchFile:                 persistedDirectoryWithPatchFileLazy{},
		persistedDirectoryLazyKindWithNewFile:                   persistedDirectoryWithNewFileLazy{},
		persistedDirectoryLazyKindWithFile:                      persistedDirectoryWithFileLazy{},
		persistedDirectoryLazyKindWithFiles:                     persistedDirectoryWithFilesLazy{},
		persistedDirectoryLazyKindWithTimestamps:                persistedDirectoryWithTimestampsLazy{},
		persistedDirectoryLazyKindWithNewDirectory:              persistedDirectoryWithNewDirectoryLazy{},
		persistedDirectoryLazyKindSubdirectory:                  persistedDirectorySubdirectoryLazy{},
		persistedDirectoryLazyKindDiff:                          persistedDirectoryDiffLazy{},
		persistedDirectoryLazyKindWithChanges:                   persistedDirectoryWithChangesLazy{},
		persistedDirectoryLazyKindWithout:                       persistedDirectoryWithoutLazy{},
		persistedDirectoryLazyKindWithSymlink:                   persistedDirectoryWithSymlinkLazy{},
		persistedDirectoryLazyKindChown:                         persistedDirectoryChownLazy{},
		persistedDirectoryLazyKindMergeChangesets:               persistedDirectoryMergeChangesetsLazy{},
		persistedDirectoryLazyKindGitCommit:                     persistedDirectoryGitCommitLazy{},
		persistedDirectoryLazyKindExtract:                       persistedDirectoryExtractLazy{},
		persistedDirectoryLazyKindWorkspacePull:                 persistedDirectoryWorkspacePullLazy{},
	},
	"file": {
		persistedFileLazyKindHTTPResolve:    persistedFileHTTPResolveLazy{},
		persistedFileLazyKindBlob:           persistedFileBlobLazy{},
		persistedFileLazyKindDirectoryFile:  persistedFileSubfileLazy{},
		persistedFileLazyKindContainerFile:  persistedContainerFileLazy{},
		persistedFileLazyKindWithName:       persistedFileWithNameLazy{},
		persistedFileLazyKindWithReplaced:   persistedFileWithReplacedLazy{},
		persistedFileLazyKindWithTimestamps: persistedFileWithTimestampsLazy{},
		persistedFileLazyKindChown:          persistedFileChownLazy{},
		persistedFileLazyKindContainerImage: persistedFileContainerImageLazy{},
		persistedFileLazyKindChangesetPatch: persistedFileChangesetPatchLazy{},
	},
	"container": {
		"withEntrypoint":                    persistedContainerWithEntrypointLazy{},
		"withoutEntrypoint":                 persistedContainerWithoutEntrypointLazy{},
		"withDefaultArgs":                   persistedContainerWithDefaultArgsLazy{},
		"withoutDefaultArgs":                persistedContainerWithoutDefaultArgsLazy{},
		"withUser":                          persistedContainerWithUserLazy{},
		"withoutUser":                       persistedContainerWithoutUserLazy{},
		"withWorkdir":                       persistedContainerWithWorkdirLazy{},
		"withoutWorkdir":                    persistedContainerWithoutWorkdirLazy{},
		"withEnvVariable":                   persistedContainerWithEnvVariableLazy{},
		"withEnvFileVariables":              persistedContainerWithEnvFileVariablesLazy{},
		"__withSystemEnvVariable":           persistedContainerWithSystemEnvVariableLazy{},
		"withVolatileVariable":              persistedContainerWithVolatileVariableLazy{},
		"withoutEnvVariable":                persistedContainerWithoutEnvVariableLazy{},
		"withoutVolatileVariable":           persistedContainerWithoutVolatileVariableLazy{},
		"withLabel":                         persistedContainerWithLabelLazy{},
		"withoutLabel":                      persistedContainerWithoutLabelLazy{},
		"__withImageConfigMetadata":         persistedContainerWithImageConfigMetadataLazy{},
		"withDockerHealthcheck":             persistedContainerWithHealthcheckLazy{},
		"withoutDockerHealthcheck":          persistedContainerWithoutHealthcheckLazy{},
		"withGPU":                           persistedContainerSetGPUsLazy{},
		"experimentalWithGPU":               persistedContainerSetGPUsLazy{},
		"experimentalWithAllGPUs":           persistedContainerSetGPUsLazy{},
		"withGpu":                           persistedContainerSetGPUsLazy{},
		"experimentalWithGpu":               persistedContainerSetGPUsLazy{},
		"experimentalWithAllGpus":           persistedContainerSetGPUsLazy{},
		"withAnnotation":                    persistedContainerWithAnnotationLazy{},
		"withoutAnnotation":                 persistedContainerWithoutAnnotationLazy{},
		"withSecretVariable":                persistedContainerWithSecretVariableLazy{},
		"withoutSecretVariable":             persistedContainerWithoutSecretVariableLazy{},
		"withServiceBinding":                persistedContainerWithServiceBindingLazy{},
		"withExposedPort":                   persistedContainerWithExposedPortLazy{},
		"withoutExposedPort":                persistedContainerWithoutExposedPortLazy{},
		"withDefaultTerminalCmd":            persistedContainerWithDefaultTerminalCmdLazy{},
		"withShell":                         persistedContainerWithDefaultTerminalCmdLazy{},
		"from":                              persistedContainerFromLazy{},
		"withRootfs":                        persistedContainerWithRootFSLazy{},
		"withDirectory":                     persistedContainerWithDirectoryLazy{},
		"withFile":                          persistedContainerWithFileLazy{},
		"withNewFile":                       persistedContainerWithFileLazy{},
		"withMountedDirectory":              persistedContainerWithMountedDirectoryLazy{},
		"withMountedFile":                   persistedContainerWithMountedFileLazy{},
		"__withMountedPathDockerfileCompat": persistedContainerWithMountedPathDockerfileCompatLazy{},
		"withMountedCache":                  persistedContainerWithMountedCacheLazy{},
		"withMountedVolume":                 persistedContainerWithMountedVolumeLazy{},
		"withMountedFiles":                  persistedContainerWithMountedFilesLazy{},
		"withMountedTemp":                   persistedContainerWithMountedTempLazy{},
		"withMountedSecret":                 persistedContainerWithMountedSecretLazy{},
		"withoutMount":                      persistedContainerWithoutMountLazy{},
		"withoutDirectory":                  persistedContainerWithoutPathLazy{},
		"withoutFile":                       persistedContainerWithoutPathLazy{},
		"withoutFiles":                      persistedContainerWithoutPathLazy{},
		"withSymlink":                       persistedContainerWithSymlinkLazy{},
		"withUnixSocket":                    persistedContainerWithUnixSocketLazy{},
		"withoutUnixSocket":                 persistedContainerWithoutUnixSocketLazy{},
		"import":                            persistedContainerImportLazy{},
		"withExec":                          persistedContainerExecLazy{},
	},
}

// persistedLazyCoverageExempt are the lazy kinds the coverage test can't fill
// generically, by table and kind, each with why.
var persistedLazyCoverageExempt = map[string]map[string]string{
	"directory": {
		persistedDirectoryLazyKindScratch: "its payload is validated as empty and holds no reference (TestLazyOperationCodecs)",
	},
}

// persistedLazyVisitorTables are the visitors of each table the coverage
// test checks.
var persistedLazyVisitorTables = map[string]map[string]persistedLazyVisitor{
	"directory": persistedDirectoryLazyVisitors,
	"file":      persistedFileLazyVisitors,
	"container": persistedContainerRecipeVisitors,
}

// TestPersistedLazyVisitorsWalkEveryStoredReference sets every stored row
// reference in each lazy operation's payload to its own number, as
// TestPersistedVisitorsWalkEveryStoredReference does for the families'
// payloads, and requires the kind's visitor to report each one: a reference a
// lazy operation's visitor skips is neither relocated nor a dependency, and
// keeps the sending engine's number on the receiving one.
func TestPersistedLazyVisitorsWalkEveryStoredReference(t *testing.T) {
	t.Parallel()
	for table, visitors := range persistedLazyVisitorTables {
		for kind := range visitors {
			_, typed := persistedLazyPayloadTypes[table][kind]
			_, exempt := persistedLazyCoverageExempt[table][kind]
			require.True(t, typed || exempt, "%s kind %s: give its payload type to persistedLazyPayloadTypes, or say in persistedLazyCoverageExempt why it can't be filled", table, kind)
		}
	}
	for table, kinds := range persistedLazyPayloadTypes {
		for kind, sample := range kinds {
			t.Run(table+"/"+kind, func(t *testing.T) {
				visitor, ok := persistedLazyVisitorTables[table][kind]
				require.True(t, ok, "no %s visitor for kind %s", table, kind)
				payload := reflect.New(reflect.TypeOf(sample)).Elem()
				var next uint64 = 1000
				stored := map[uint64]string{}
				fillPersistedReferences(payload, "", &next, stored, nil)
				if len(stored) == 0 {
					// Nothing stored, nothing to miss; a visitor that only
					// validates, such as an HTTP file's, would refuse the
					// empty fields.
					return
				}
				raw, err := json.Marshal(payload.Interface())
				require.NoError(t, err)
				visited := map[uint64]bool{}
				_, err = visitor(raw, newPersistedRefWalker(func(ref *dagql.PersistedRef) error {
					visited[ref.ResultID] = true
					return nil
				}, dagql.PersistedRefPath{}))
				require.NoError(t, err)
				var missed []string
				for id, path := range stored {
					if !visited[id] {
						missed = append(missed, path)
					}
				}
				slices.Sort(missed)
				require.Empty(t, missed, "%s kind %s stores these references, which its visitor doesn't walk", table, kind)
			})
		}
	}
}
