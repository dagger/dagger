package client

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func diffOpTestCases() (tests []integration.Test) {
	alpine := func() llb.State { return llb.Image("alpine:latest", llb.ResolveDigest(true)) }
	// busybox doesn't have /proc or /sys in its base image, add them
	// so they don't show up in every diff of an exec on it
	busybox := func() llb.State {
		return llb.Image("busybox:latest", llb.ResolveDigest(true)).
			File(llb.Mkdir("/proc", 0755)).
			File(llb.Mkdir("/sys", 0755))
	}

	// Diffs of identical states are empty
	tests = append(tests,
		verifyContents{
			name:     "TestDiffScratch",
			state:    llb.Diff(llb.Scratch(), llb.Scratch()),
			contents: empty,
		},
		verifyContents{
			name:     "TestDiffSelf",
			state:    llb.Diff(alpine(), alpine()),
			contents: empty,
		},
		verifyContents{
			name:     "TestDiffSelfDeletes",
			state:    llb.Merge([]llb.State{alpine(), llb.Diff(alpine(), alpine())}),
			contents: contentsOf(alpine()),
		},
	)

	// Diff of state with scratch has same contents as the state
	tests = append(tests,
		verifyContents{
			name:     "TestDiffLowerScratch",
			state:    llb.Diff(llb.Scratch(), alpine()),
			contents: contentsOf(alpine()),
		},
		verifyContents{
			name: "TestDiffLowerScratchDeletes",
			state: llb.Merge([]llb.State{
				llb.Scratch().
					File(llb.Mkfile("/foo", 0755, []byte("A"))),
				llb.Diff(llb.Scratch(), llb.Scratch().
					File(llb.Mkfile("/foo", 0644, []byte("B"))).
					File(llb.Rm("/foo")).
					File(llb.Mkfile("/bar", 0644, nil))),
			}),
			contents: apply(
				fstest.CreateFile("/bar", nil, 0644),
			),
		},
	)

	// Diff from a state to scratch is just a deletion of the contents of that state
	tests = append(tests,
		verifyContents{
			name: "TestDiffUpperScratch",
			state: llb.Merge([]llb.State{
				alpine(),
				llb.Scratch().File(llb.Mkfile("/foo", 0644, []byte("foo"))),
				llb.Diff(alpine(), llb.Scratch()),
			}),
			contents: apply(
				fstest.CreateFile("/foo", []byte("foo"), 0644),
			),
		},
	)

	// Basic diff slices
	tests = append(tests, func() (tests []integration.Test) {
		base := func() llb.State {
			return alpine().
				File(llb.Mkfile("/shuffleFile1", 0644, []byte("shuffleFile1"))).
				File(llb.Mkdir("/shuffleDir1", 0755)).
				File(llb.Mkdir("/shuffleDir1/shuffleSubdir1", 0755)).
				File(llb.Mkfile("/shuffleDir1/shuffleSubfile1", 0644, nil)).
				File(llb.Mkfile("/shuffleDir1/shuffleSubdir1/shuffleSubfile2", 0644, nil)).
				File(llb.Mkdir("/unmodifiedDir", 0755)).
				File(llb.Mkdir("/unmodifiedDir/chmodSubdir1", 0755)).
				File(llb.Mkdir("/unmodifiedDir/deleteSubdir1", 0755)).
				File(llb.Mkdir("/unmodifiedDir/opaqueDir1", 0755)).
				File(llb.Mkdir("/unmodifiedDir/opaqueDir1/opaqueSubdir1", 0755)).
				File(llb.Mkdir("/unmodifiedDir/overrideSubdir1", 0755)).
				File(llb.Mkdir("/unmodifiedDir/shuffleDir2", 0755)).
				File(llb.Mkdir("/unmodifiedDir/shuffleDir2/shuffleSubdir2", 0755)).
				File(llb.Mkfile("/unmodifiedDir/chmodFile1", 0644, []byte("chmodFile1"))).
				File(llb.Mkfile("/unmodifiedDir/modifyContentFile1", 0644, []byte("modifyContentFile1"))).
				File(llb.Mkfile("/unmodifiedDir/deleteFile1", 0644, nil)).
				File(llb.Mkfile("/unmodifiedDir/opaqueDir1/opaqueFile1", 0644, nil)).
				File(llb.Mkfile("/unmodifiedDir/opaqueDir1/opaqueSubdir1/opaqueFile2", 0644, nil)).
				File(llb.Mkfile("/unmodifiedDir/overrideFile1", 0644, nil)).
				File(llb.Mkfile("/unmodifiedDir/overrideFile2", 0644, nil)).
				File(llb.Mkfile("/unmodifiedDir/shuffleFile2", 0644, []byte("shuffleFile2"))).
				File(llb.Mkfile("/unmodifiedDir/shuffleDir2/shuffleSubfile3", 0644, nil)).
				File(llb.Mkfile("/unmodifiedDir/shuffleDir2/shuffleSubdir2/shuffleSubfile4", 0644, nil)).
				File(llb.Mkdir("/modifyDir", 0755)).
				File(llb.Mkdir("/modifyDir/chmodSubdir2", 0755)).
				File(llb.Mkdir("/modifyDir/deleteSubdir2", 0755)).
				File(llb.Mkdir("/modifyDir/opaqueDir2", 0755)).
				File(llb.Mkdir("/modifyDir/opaqueDir2/opaqueSubdir2", 0755)).
				File(llb.Mkdir("/modifyDir/overrideSubdir2", 0755)).
				File(llb.Mkdir("/modifyDir/shuffleDir3", 0755)).
				File(llb.Mkdir("/modifyDir/shuffleDir3/shuffleSubdir3", 0755)).
				File(llb.Mkfile("/modifyDir/chmodFile2", 0644, []byte("chmodFile2"))).
				File(llb.Mkfile("/modifyDir/modifyContentFile2", 0644, []byte("modifyContentFile2"))).
				File(llb.Mkfile("/modifyDir/deleteFile2", 0644, nil)).
				File(llb.Mkfile("/modifyDir/opaqueDir2/opaqueFile3", 0644, nil)).
				File(llb.Mkfile("/modifyDir/opaqueDir2/opaqueSubdir2/opaqueFile4", 0644, nil)).
				File(llb.Mkfile("/modifyDir/overrideFile3", 0644, nil)).
				File(llb.Mkfile("/modifyDir/overrideFile4", 0644, nil)).
				File(llb.Mkfile("/modifyDir/shuffleFile3", 0644, []byte("shuffleFile3"))).
				File(llb.Mkfile("/modifyDir/shuffleDir3/shuffleSubfile4", 0644, nil)).
				File(llb.Mkfile("/modifyDir/shuffleDir3/shuffleSubdir3/shuffleSubfile6", 0644, nil))
		}

		joinCmds := func(cmds ...[]string) []string {
			var all []string
			for _, cmd := range cmds {
				all = append(all, cmd...)
			}
			return all
		}

		var allCmds [][]string
		var allContents []contents

		baseDiffCmds := []string{
			"chmod 0700 /modifyDir",
		}
		baseDiffContents := apply(
			fstest.CreateDir("/unmodifiedDir", 0755),
			fstest.CreateDir("/modifyDir", 0700),
		)
		allCmds = append(allCmds, baseDiffCmds)
		allContents = append(allContents, baseDiffContents)

		newFileCmds := []string{
			// Create a new file under a new dir
			"mkdir /newdir1",
			"touch /newdir1/newfile1",
			// Create a new file under an unmodified existing dir
			"touch /unmodifiedDir/newfile2",
			// Create a new file under a modified existing dir
			"touch /modifyDir/newfile3",
		}
		newFileContents := apply(
			fstest.CreateDir("/newdir1", 0755),
			fstest.CreateFile("/newdir1/newfile1", nil, 0644),
			fstest.CreateFile("/unmodifiedDir/newfile2", nil, 0644),
			fstest.CreateFile("/modifyDir/newfile3", nil, 0644),
		)
		allCmds = append(allCmds, newFileCmds)
		allContents = append(allContents, newFileContents)
		tests = append(tests, verifyContents{
			name: "TestDiffNewFiles",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				newFileCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				newFileContents,
			),
		})

		modifyFileCmds := []string{
			// Modify an existing file under an unmodified existing dir
			"chmod 0444 /unmodifiedDir/chmodFile1",
			"echo -n modifyContentFile0 > /unmodifiedDir/modifyContentFile1",

			// Modify an existing file under a modified existing dir
			"chmod 0440 /modifyDir/chmodFile2",
			"echo -n modifyContentFile0 > /modifyDir/modifyContentFile2",
		}
		modifyFileContents := apply(
			fstest.CreateFile("/unmodifiedDir/chmodFile1", []byte("chmodFile1"), 0444),
			fstest.CreateFile("/unmodifiedDir/modifyContentFile1", []byte("modifyContentFile0"), 0644),

			fstest.CreateFile("/modifyDir/chmodFile2", []byte("chmodFile2"), 0440),
			fstest.CreateFile("/modifyDir/modifyContentFile2", []byte("modifyContentFile0"), 0644),
		)
		allCmds = append(allCmds, modifyFileCmds)
		allContents = append(allContents, modifyFileContents)
		tests = append(tests, verifyContents{
			name: "TestDiffModifiedFiles",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				modifyFileCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				modifyFileContents,
			),
		})

		createNewDirCmds := []string{
			// Create a new dir under a new dir
			"mkdir -p /newdir2/newsubdir1",

			// Create a new dir under an unmodified existing dir
			"mkdir /unmodifiedDir/newsubdir2",

			// Create a new dir under a modified existing dir
			"mkdir /modifyDir/newsubdir3",
		}
		createNewDirContents := apply(
			fstest.CreateDir("/newdir2", 0755),
			fstest.CreateDir("/newdir2/newsubdir1", 0755),
			fstest.CreateDir("/unmodifiedDir/newsubdir2", 0755),
			fstest.CreateDir("/modifyDir/newsubdir3", 0755),
		)
		allCmds = append(allCmds, createNewDirCmds)
		allContents = append(allContents, createNewDirContents)
		tests = append(tests, verifyContents{
			name: "TestDiffNewDirs",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				createNewDirCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				createNewDirContents,
			),
		})

		modifyDirCmds := []string{
			// Modify a dir under an unmodified existing dir
			"chmod 0700 /unmodifiedDir/chmodSubdir1",

			// Modify a dir under an modified existing dir
			"chmod 0770 /modifyDir/chmodSubdir2",
		}
		modifyDirContents := apply(
			fstest.CreateDir("/unmodifiedDir/chmodSubdir1", 0700),
			fstest.CreateDir("/modifyDir/chmodSubdir2", 0770),
		)
		allCmds = append(allCmds, modifyDirCmds)
		allContents = append(allContents, modifyDirContents)
		tests = append(tests, verifyContents{
			name: "TestDiffModifiedDirs",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				modifyDirCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				modifyDirContents,
			),
		})

		overrideDirCmds := []string{
			"rm -rf /unmodifiedDir/overrideSubdir1",
			"echo -n overrideSubdir1 > /unmodifiedDir/overrideSubdir1",

			"rm -rf /modifyDir/overrideSubdir2",
			"echo -n overrideSubdir2 > /modifyDir/overrideSubdir2",
		}
		overrideDirContents := apply(
			fstest.CreateFile("/unmodifiedDir/overrideSubdir1", []byte("overrideSubdir1"), 0644),
			fstest.CreateFile("/modifyDir/overrideSubdir2", []byte("overrideSubdir2"), 0644),
		)
		allCmds = append(allCmds, overrideDirCmds)
		allContents = append(allContents, overrideDirContents)
		tests = append(tests, verifyContents{
			name: "TestDiffOverrideDirs",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				overrideDirCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				overrideDirContents,
			),
		})

		overrideFileCmds := []string{
			"rm /unmodifiedDir/overrideFile1",
			"mkdir -m 0700 /unmodifiedDir/overrideFile1",
			"rm /unmodifiedDir/overrideFile2",
			"mkdir -m 0750 /unmodifiedDir/overrideFile2",
			"mkdir -m 0770 /unmodifiedDir/overrideFile2/newsubdir4",
			"touch /unmodifiedDir/overrideFile2/newfile4",
			"touch /unmodifiedDir/overrideFile2/newsubdir4/newfile5",

			"rm /modifyDir/overrideFile3",
			"mkdir -m 0700 /modifyDir/overrideFile3",
			"rm /modifyDir/overrideFile4",
			"mkdir -m 0750 /modifyDir/overrideFile4",
			"mkdir -m 0770 /modifyDir/overrideFile4/newsubdir5",
			"touch /modifyDir/overrideFile4/newfile6",
			"touch /modifyDir/overrideFile4/newsubdir5/newfile7",
		}
		overrideFileContents := apply(
			fstest.CreateDir("/unmodifiedDir/overrideFile1", 0700),
			fstest.CreateDir("/unmodifiedDir/overrideFile2", 0750),
			fstest.CreateDir("/unmodifiedDir/overrideFile2/newsubdir4", 0770),
			fstest.CreateFile("/unmodifiedDir/overrideFile2/newfile4", nil, 0644),
			fstest.CreateFile("/unmodifiedDir/overrideFile2/newsubdir4/newfile5", nil, 0644),

			fstest.CreateDir("/modifyDir/overrideFile3", 0700),
			fstest.CreateDir("/modifyDir/overrideFile4", 0750),
			fstest.CreateDir("/modifyDir/overrideFile4/newsubdir5", 0770),
			fstest.CreateFile("/modifyDir/overrideFile4/newfile6", nil, 0644),
			fstest.CreateFile("/modifyDir/overrideFile4/newsubdir5/newfile7", nil, 0644),
		)
		allCmds = append(allCmds, overrideFileCmds)
		allContents = append(allContents, overrideFileContents)
		tests = append(tests, verifyContents{
			name: "TestDiffOverrideFiles",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				overrideFileCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				overrideFileContents,
			),
		})

		deleteFileCmds := []string{
			"rm /unmodifiedDir/deleteFile1",
			"rm /modifyDir/deleteFile2",
		}
		allCmds = append(allCmds, deleteFileCmds)
		tests = append(tests, verifyContents{
			name: "TestDiffDeleteFiles",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				deleteFileCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
			),
		})
		tests = append(tests, verifyContents{
			name: "TestDiffDeleteFilesMerge",
			state: llb.Merge([]llb.State{
				base(),
				llb.Diff(base(), runShell(base(), joinCmds(
					baseDiffCmds,
					deleteFileCmds,
				)...)),
			}),
			contents: mergeContents(
				contentsOf(base()),
				baseDiffContents,
				apply(
					fstest.Remove("/unmodifiedDir/deleteFile1"),
					fstest.Remove("/modifyDir/deleteFile2"),
				),
			),
		})

		deleteDirCmds := []string{
			"rm -rf /unmodifiedDir/deleteSubdir1",
			"rm -rf /modifyDir/deleteSubdir2",
		}
		allCmds = append(allCmds, deleteDirCmds)
		tests = append(tests, verifyContents{
			name: "TestDiffDeleteDirs",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				deleteDirCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
			),
		})
		tests = append(tests, verifyContents{
			name: "TestDiffDeleteDirsMerge",
			state: llb.Merge([]llb.State{
				base(),
				llb.Diff(base(), runShell(base(), joinCmds(
					baseDiffCmds,
					deleteDirCmds,
				)...)),
			}),
			contents: mergeContents(
				contentsOf(base()),
				baseDiffContents,
				apply(
					fstest.RemoveAll("/unmodifiedDir/deleteSubdir1"),
					fstest.RemoveAll("/modifyDir/deleteSubdir2"),
				),
			),
		})

		basePlusExtra := func() llb.State {
			return base().
				File(llb.Mkdir("/extradir", 0755)).
				File(llb.Mkfile("/extradir/extrafile", 0755, nil))
		}
		tests = append(tests, verifyContents{
			name: "TestDiffUnmatchedDelete",
			state: llb.Merge([]llb.State{
				base(),
				llb.Diff(basePlusExtra(), basePlusExtra().File(llb.Rm("/extradir/extrafile"))),
			}),
			contents: mergeContents(
				contentsOf(base()),
				apply(
					// Surprisingly, it's expected that /extradir shows up in the diff.
					// This is the behavior of the exporter, so we have to enforce
					// consistency with it.
					// https://github.com/containerd/containerd/pull/2095
					fstest.CreateDir("/extradir", 0755),
				),
			),
		})

		// Check that deleting together a file from the base and another one from a merge
		// does not result in a crash.
		deleteFileAfterMergeCmds := []string{
			"rm /unmodifiedDir/deleteFile1 /unmodifiedDir/deleteFile2",
		}

		extraContent := llb.Scratch().
			File(llb.Mkdir("/unmodifiedDir", 0755)).
			File(llb.Mkfile("/unmodifiedDir/deleteFile2", 0644, []byte("foo")))

		tests = append(tests, verifyContents{
			name: "TestDiffDeleteFilesAfterMerge",
			state: llb.Diff(
				base(),
				runShell(llb.Merge([]llb.State{
					base(),
					extraContent,
				}),
					joinCmds(
						deleteFileAfterMergeCmds,
					)...)),
			contents: mergeContents(
				apply(
					fstest.CreateDir("/unmodifiedDir", 0755)),
			),
		})

		// Opaque dirs should be converted to the "explicit whiteout" format, as described in the OCI image spec:
		// https://github.com/opencontainers/image-spec/blob/main/layer.md#opaque-whiteout
		opaqueDirCmds := []string{
			"rm -rf /unmodifiedDir/opaqueDir1",
			"mkdir -p /unmodifiedDir/opaqueDir1/newOpaqueSubdir1",
			"touch /unmodifiedDir/opaqueDir1/newOpaqueFile1",
			"touch /unmodifiedDir/opaqueDir1/newOpaqueSubdir1/newOpaqueFile2",

			"rm -rf /modifyDir/opaqueDir2",
			"mkdir -p /modifyDir/opaqueDir2/newOpaqueSubdir2",
			"touch /modifyDir/opaqueDir2/newOpaqueFile3",
			"touch /modifyDir/opaqueDir2/newOpaqueSubdir2/newOpaqueFile4",
		}
		opaqueDirContents := apply(
			fstest.CreateDir("/unmodifiedDir/opaqueDir1", 0755),
			fstest.CreateDir("/unmodifiedDir/opaqueDir1/newOpaqueSubdir1", 0755),
			fstest.CreateFile("/unmodifiedDir/opaqueDir1/newOpaqueFile1", nil, 0644),
			fstest.CreateFile("/unmodifiedDir/opaqueDir1/newOpaqueSubdir1/newOpaqueFile2", nil, 0644),

			fstest.CreateDir("/modifyDir/opaqueDir2", 0755),
			fstest.CreateDir("/modifyDir/opaqueDir2/newOpaqueSubdir2", 0755),
			fstest.CreateFile("/modifyDir/opaqueDir2/newOpaqueFile3", nil, 0644),
			fstest.CreateFile("/modifyDir/opaqueDir2/newOpaqueSubdir2/newOpaqueFile4", nil, 0644),
		)
		allCmds = append(allCmds, opaqueDirCmds)
		allContents = append(allContents, opaqueDirContents)
		tests = append(tests, verifyContents{
			name: "TestDiffOpaqueDirs",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				opaqueDirCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				opaqueDirContents,
			),
		})
		tests = append(tests, verifyContents{
			name: "TestDiffOpaqueDirsMerge",
			state: llb.Merge([]llb.State{
				base().
					File(llb.Mkfile("/unmodifiedDir/opaqueDir1/rebaseFile1", 0644, nil)).
					File(llb.Mkfile("/unmodifiedDir/opaqueDir1/opaqueSubdir1/rebaseFile2", 0644, nil)).
					File(llb.Mkfile("/modifyDir/opaqueDir2/rebaseFile3", 0644, nil)).
					File(llb.Mkfile("/modifyDir/opaqueDir2/opaqueSubdir2/rebaseFile4", 0644, nil)),
				llb.Diff(base(), runShell(base(), joinCmds(
					baseDiffCmds,
					opaqueDirCmds,
				)...)),
			}),
			contents: mergeContents(
				contentsOf(base()),
				baseDiffContents,
				apply(
					fstest.RemoveAll("/unmodifiedDir/opaqueDir1"),
					fstest.RemoveAll("/modifyDir/opaqueDir2"),
				),
				opaqueDirContents,
				apply(
					fstest.CreateFile("/unmodifiedDir/opaqueDir1/rebaseFile1", nil, 0644),
					fstest.CreateFile("/modifyDir/opaqueDir2/rebaseFile3", nil, 0644),
				),
			),
		})

		// Test that shuffling files back and forth without making any actual changes results
		// in no diff. This requires careful handling in the overlay differ, which can be easily
		// tricked into thinking this is a diff because the shuffled files will show up in the
		// upper dir.
		// Note that this is only tested on files and not directories because overlay mounts
		// that don't have redirect_dir=on will fail any attempt to make the rename syscall
		// on a directory with EXDEV. In theory, busybox's mv command is supposed to handle
		// this transparently, but it doesn't support setting nanosecond timestamps when falling
		// back to a copy, so the mv'd files have truncated timestamps that cause the differ
		// to think there is a diff.
		shuffleFileCmds := []string{
			// Shuffle a file under root
			"mv /shuffleFile1 /shuffleFile1tmp",
			"mv /shuffleFile1tmp /shuffleFile1",

			// Shuffle a file under an unmodified existing dir
			"mv /unmodifiedDir/shuffleFile2 /unmodifiedDir/shuffleFile2tmp",
			"mv /unmodifiedDir/shuffleFile2tmp /unmodifiedDir/shuffleFile2",

			// Shuffle a file under a modified existing dir
			"mv /modifyDir/shuffleFile3 /modifyDir/shuffleFile3tmp",
			"mv /modifyDir/shuffleFile3tmp /modifyDir/shuffleFile3",
		}
		allCmds = append(allCmds, shuffleFileCmds)
		tests = append(tests, verifyContents{
			name: "TestDiffShuffleFiles",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				shuffleFileCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				apply(fstest.RemoveAll("/unmodifiedDir")),
			),
		})
		tests = append(tests, verifyContents{
			name: "TestDiffShuffleFilesMerge",
			state: llb.Merge([]llb.State{
				base(),
				llb.Diff(base(), runShell(base(), joinCmds(
					baseDiffCmds,
					shuffleFileCmds,
				)...)),
			}),
			contents: mergeContents(
				contentsOf(base()),
				baseDiffContents,
			),
		})

		// verify that fifos and devices are handled
		// TODO: test sockets. Not straightforward because there's no builtin commands in alpine or busybox
		// that create sockets except for daemons like udhcpd... Maybe you could mount a socket and then copy
		// from within the container?
		mknodFifosCmds := []string{
			"mknod /unmodifiedDir/fifo1 p",
			"mknod /modifyDir/fifo2 p",
		}
		mknodFifosContents := apply(
			mkfifo("/unmodifiedDir/fifo1", 0644),
			mkfifo("/modifyDir/fifo2", 0644),
		)
		allCmds = append(allCmds, mknodFifosCmds)
		allContents = append(allContents, mknodFifosContents)
		tests = append(tests, verifyContents{
			name: "TestDiffFifos",
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				mknodFifosCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				mknodFifosContents,
			),
		})

		mknodChardevCmds := []string{
			"mknod /unmodifiedDir/null1 c 1 3",
			"mknod /modifyDir/null2 c 1 3",
		}
		mknodChardevContents := apply(
			mkchardev("/unmodifiedDir/null1", 0644, 1, 3),
			mkchardev("/modifyDir/null2", 0644, 1, 3),
		)
		tests = append(tests, verifyContents{
			name:           "TestDiffChardevs",
			skipOnRootless: true, // you need root user namespace privilege to create device nodes
			state: llb.Diff(base(), runShell(base(), joinCmds(
				baseDiffCmds,
				mknodChardevCmds,
			)...)),
			contents: mergeContents(
				baseDiffContents,
				mknodChardevContents,
			),
		})

		// combine all the previous tests into one to make sure the diffs can be calculated
		// together equivalently
		var flattenedCmds []string
		for _, cmds := range allCmds {
			flattenedCmds = append(flattenedCmds, cmds...)
		}
		tests = append(tests, verifyContents{
			name:     "TestDiffCombinedSingleLayer",
			state:    llb.Diff(base(), runShell(base(), flattenedCmds...)),
			contents: mergeContents(allContents...),
		})

		tests = append(tests, verifyContents{
			name:     "TestDiffCombinedMultiLayer",
			state:    llb.Diff(base(), chainRunShells(base(), allCmds...)),
			contents: mergeContents(allContents...),
		})
		return tests
	}()...)

	tests = append(tests, func() []integration.Test {
		base := func() llb.State {
			return runShell(alpine(),
				"mkdir -p /parentdir/dir/subdir",
				"touch /parentdir/dir/A /parentdir/dir/B /parentdir/dir/subdir/C",
			)
		}
		return []integration.Test{
			verifyContents{
				name: "TestDiffOpaqueDirs",
				state: llb.Merge([]llb.State{
					runShell(busybox(),
						"mkdir -p /parentdir/dir/subdir",
						"touch /parentdir/dir/A /parentdir/dir/B /parentdir/dir/D",
					),
					llb.Diff(base(), runShell(base(),
						"rm -rf /parentdir/dir",
						"mkdir /parentdir/dir",
						"touch /parentdir/dir/E",
					)),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir -p /parentdir/dir",
					"touch /parentdir/dir/D",
					"touch /parentdir/dir/E",
				)),
			},
		}
	}()...)

	// Symlink handling tests
	tests = append(tests, func() []integration.Test {
		linkFooToBar := func() llb.State {
			return llb.Diff(alpine(), runShell(alpine(), "mkdir -p /bar", "ln -s /bar /foo"))
		}

		alpinePlusFoo := func() llb.State {
			return runShell(alpine(), "mkdir /foo")
		}
		deleteFoo := func() llb.State {
			return llb.Diff(alpinePlusFoo(), runShell(alpinePlusFoo(), "rm -rf /foo"))
		}
		createFooFile := func() llb.State {
			return llb.Diff(alpinePlusFoo(), runShell(alpinePlusFoo(), "touch /foo/file"))
		}

		return []integration.Test{
			verifyContents{
				name: "TestDiffDirOverridesSymlink",
				state: llb.Merge([]llb.State{
					busybox(),
					linkFooToBar(),
					createFooFile(),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir /bar",
					"mkdir /foo",
					"touch /foo/file",
				)),
			},
			verifyContents{
				name: "TestDiffSymlinkOverridesDir",
				state: llb.Merge([]llb.State{
					busybox(),
					createFooFile(),
					linkFooToBar(),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir /bar",
					"ln -s /bar /foo",
				)),
			},
			verifyContents{
				name: "TestDiffSymlinkOverridesSymlink",
				state: llb.Merge([]llb.State{
					busybox(),
					llb.Diff(alpine(), runShell(alpine(),
						"mkdir /1 /2",
						"ln -s /1 /a",
						"ln -s /2 /a/b",
					)),
					llb.Diff(alpine(), runShell(alpine(),
						"mkdir /3",
						"ln -s /3 /a",
					)),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir /1 /2 /3",
					"ln -s /3 /a",
					"ln -s /2 /1/b",
				)),
			},

			verifyContents{
				name: "TestDiffDeleteDoesNotFollowSymlink",
				state: llb.Merge([]llb.State{
					busybox(),
					linkFooToBar(),
					deleteFoo(),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir /bar",
				)),
			},
			verifyContents{
				name: "TestDiffDeleteDoesNotFollowParentSymlink",
				state: llb.Merge([]llb.State{
					busybox(),
					linkFooToBar().File(llb.Mkfile("/bar/file", 0644, nil)),
					llb.Diff(createFooFile(), createFooFile().File(llb.Rm("/foo/file"))),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir /bar",
					"touch /bar/file",
					"mkdir /foo",
				)),
			},

			verifyContents{
				name: "TestDiffCircularSymlinks",
				state: llb.Merge([]llb.State{
					busybox(),
					llb.Diff(alpine(), runShell(alpine(), "ln -s /2 /1", "ln -s /1 /2")),
					llb.Scratch().
						File(llb.Mkfile("/1", 0644, []byte("foo"))),
				}),
				contents: contentsOf(runShell(busybox(),
					"echo -n foo > /1",
					"ln -s /1 /2",
				)),
			},
			verifyContents{
				name: "TestDiffCircularDirSymlink",
				state: llb.Merge([]llb.State{
					busybox(),
					llb.Diff(alpine(), runShell(alpine(), "mkdir /foo", "ln -s ../foo /foo/link")),
					llb.Diff(alpine(), runShell(alpine(), "mkdir -p /foo/link", "touch /foo/link/file")),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir -p /foo/link",
					"touch /foo/link/file",
				)),
			},
			verifyContents{
				name: "TestDiffCircularParentDirSymlinks",
				state: llb.Merge([]llb.State{
					busybox(),
					llb.Diff(alpine(), runShell(alpine(), "ln -s /2 /1", "ln -s /1 /2")),
					llb.Diff(alpine(), runShell(alpine(), "mkdir /1", "echo -n foo > /1/file")),
				}),
				contents: contentsOf(runShell(busybox(),
					"mkdir /1",
					"echo -n foo > /1/file",
					"ln -s /1 /2",
				)),
			},
		}
	}()...)

	// Test hardlinks
	// NOTE: There are long-standing inconsistencies in hardlink handling between overlay snapshotters
	// and the native snapshotter that have to be avoided here. Namely, when a hardlink is copied-up
	// by overlay, the link will be broken unless the inodes index feature is enabled (which we can't
	// enabled because it disallows you from moving overlay layers ontop of different ones from which
	// they were originally created). See the overlay docs for more details:
	// https://www.kernel.org/doc/html/latest/filesystems/overlayfs.html?highlight=overlayfs#non-standard-behavior
	tests = append(tests, func() []integration.Test {
		linkedFiles := func() llb.State {
			return llb.Diff(alpine(), runShell(alpine(),
				"mkdir /dir",
				"touch /dir/1",
				"ln /dir/1 /dir/2",
				"chmod 0600 /dir/2",
			))
		}
		mntB := func() llb.State {
			chmodExecState := runShellExecState(busybox(), "chmod 0777 /A/dir/1")
			chmodExecState.AddMount("/A", linkedFiles())
			return chmodExecState.AddMount("/B", linkedFiles())
		}
		return []integration.Test{
			verifyContents{
				name:  "TestDiffHardlinks",
				state: linkedFiles(),
				contents: apply(
					fstest.CreateDir("/dir", 0755),
					fstest.CreateFile("/dir/1", nil, 0600),
					fstest.Link("/dir/1", "/dir/2"),
				),
			},
			verifyContents{
				name:  "TestDiffHardlinkChangesDoNotPropagateBetweenSnapshots",
				state: mntB(),
				contents: apply(
					fstest.CreateDir("/dir", 0755),
					fstest.CreateFile("/dir/1", nil, 0600),
					fstest.Link("/dir/1", "/dir/2"),
				),
			},
		}
	}()...)

	// Diffs of lazy blobs should work.
	tests = append(tests,
		verifyContents{
			name: "TestDiffLazyBlobMerge",
			// Merge(A, Diff(A,B)) == B
			state:    llb.Merge([]llb.State{busybox(), llb.Diff(busybox(), alpine())}),
			contents: contentsOf(alpine()),
		},
	)

	// Diffs of exec mounts should only include the changes made under the mount
	tests = append(tests, func() []integration.Test {
		splitDiffExecState := func() llb.ExecState {
			execState := runShellExecState(alpine(), "touch /root/A", "touch /mnt/B")
			execState.AddMount("/mnt", busybox())
			return execState
		}
		return []integration.Test{
			verifyContents{
				name:  "TestDiffExecRoot",
				state: llb.Diff(alpine(), splitDiffExecState().Root()),
				contents: apply(
					fstest.CreateDir("/root", 0700),
					fstest.CreateFile("/root/A", nil, 0644),
				),
			},
			verifyContents{
				name:  "TestDiffExecMount",
				state: llb.Diff(busybox(), splitDiffExecState().GetMount("/mnt")),
				contents: apply(
					fstest.CreateFile("/B", nil, 0644),
				),
			},
		}
	}()...)

	// Diff+Merge combinations
	tests = append(tests, func() []integration.Test {
		a := func() llb.State {
			return llb.Scratch().File(llb.Mkfile("A", 0644, []byte("A")))
		}
		b := func() llb.State {
			return llb.Scratch().File(llb.Mkfile("B", 0644, []byte("B")))
		}
		c := func() llb.State {
			return llb.Scratch().File(llb.Mkfile("C", 0644, []byte("C")))
		}
		deleteC := func() llb.State {
			return c().File(llb.Rm("C"))
		}

		ab := func() llb.State {
			return llb.Merge([]llb.State{a(), b()})
		}
		abc := func() llb.State {
			return llb.Merge([]llb.State{a(), b(), c()})
		}
		abDeleteC := func() llb.State {
			return llb.Merge([]llb.State{a(), b(), deleteC()})
		}

		// nested is abcdae-a
		nested := func() llb.State {
			return llb.Merge([]llb.State{
				abc().File(llb.Mkfile("D", 0644, []byte("D"))),
				llb.Merge([]llb.State{
					a(),
					llb.Scratch().File(llb.Mkfile("E", 0644, []byte("E"))),
				}).File(llb.Rm("A")),
			})
		}
		return []integration.Test{
			verifyContents{
				name: "TestDiffOnlyMerge",
				state: llb.Merge([]llb.State{
					llb.Diff(a(), b()),
					llb.Diff(b(), a()),
				}),
				contents: contentsOf(a()),
			},

			verifyContents{
				name:  "TestDiffOfMerges",
				state: llb.Diff(ab(), abc()),
				contents: apply(
					fstest.CreateFile("/C", []byte("C"), 0644),
				),
			},
			verifyContents{
				name:  "TestDiffOfMergesWithDeletes",
				state: llb.Merge([]llb.State{abc(), llb.Diff(abc(), abDeleteC())}),
				contents: apply(
					fstest.CreateFile("/A", []byte("A"), 0644),
					fstest.CreateFile("/B", []byte("B"), 0644),
				),
			},

			verifyContents{
				name:  "TestDiffSingleLayerOnMerge",
				state: llb.Diff(abDeleteC(), abDeleteC().File(llb.Mkfile("D", 0644, []byte("D")))),
				contents: apply(
					fstest.CreateFile("/D", []byte("D"), 0644),
				),
			},
			verifyContents{
				name: "TestDiffSingleDeleteLayerOnMerge",
				state: llb.Merge([]llb.State{
					abDeleteC(),
					llb.Diff(abc(), abc().File(llb.Rm("A"))),
				}),
				contents: apply(
					fstest.CreateFile("/B", []byte("B"), 0644),
				),
			},
			verifyContents{
				name: "TestDiffMultipleLayerOnMerge",
				state: llb.Merge([]llb.State{
					abDeleteC(),
					llb.Diff(abc(), abc().
						File(llb.Mkfile("D", 0644, []byte("D"))).
						File(llb.Rm("A")),
					),
				}),
				contents: apply(
					fstest.CreateFile("/B", []byte("B"), 0644),
					fstest.CreateFile("/D", []byte("D"), 0644),
				),
			},

			verifyContents{
				name:  "TestDiffNestedLayeredMerges",
				state: llb.Diff(abc(), nested().File(llb.Mkfile("F", 0644, []byte("F")))),
				contents: apply(
					fstest.CreateFile("/D", []byte("D"), 0644),
					fstest.CreateFile("/E", []byte("E"), 0644),
					fstest.CreateFile("/F", []byte("F"), 0644),
				),
			},
			verifyContents{
				name: "TestDiffNestedLayeredMergeDeletes",
				// this is "ab" + "d" + Diff("abc", "abcdae-a"+"-d") == "abd" + "dae-a-d" == abddae-a-d
				state: llb.Merge([]llb.State{
					ab().File(llb.Mkfile("D", 0644, []byte("D"))),
					llb.Diff(abc(), nested().File(llb.Rm("D"))),
				}),
				contents: apply(
					fstest.CreateFile("/B", []byte("B"), 0644),
					fstest.CreateFile("/E", []byte("E"), 0644),
				),
			},
		}
	}()...)

	tests = append(tests, func() []integration.Test {
		a := func() llb.State {
			return llb.Scratch().File(llb.Mkfile("A", 0644, []byte("A")))
		}
		ab := func() llb.State {
			return a().File(llb.Mkfile("B", 0644, []byte("B")))
		}
		abc := func() llb.State {
			return ab().File(llb.Mkfile("C", 0644, []byte("C")))
		}
		return []integration.Test{
			// Diffs of diffs
			verifyContents{
				name: "TestDiffOfDiffs",
				state: llb.Diff(
					llb.Diff(a(), ab()),
					llb.Diff(a(), abc()),
				),
				contents: apply(
					fstest.CreateFile("/C", []byte("C"), 0644),
				),
			},
			verifyContents{
				name: "TestDiffOfDiffsWithDeletes",
				state: llb.Merge([]llb.State{
					abc(),
					llb.Diff(
						llb.Diff(a(), abc()),
						llb.Diff(a(), ab()),
					),
				}),
				contents: apply(
					fstest.CreateFile("/A", []byte("A"), 0644),
					fstest.CreateFile("/B", []byte("B"), 0644),
				),
			},

			// Diffs can be used as layer parents
			verifyContents{
				name:  "TestDiffAsParentSingleLayer",
				state: llb.Diff(a(), ab()).File(llb.Mkfile("D", 0644, []byte("D"))),
				contents: apply(
					fstest.CreateFile("B", []byte("B"), 0644),
					fstest.CreateFile("D", []byte("D"), 0644),
				),
			},
			verifyContents{
				name: "TestDiffAsParentSingleLayerDelete",
				state: llb.Merge([]llb.State{
					ab(),
					llb.Diff(a(), ab()).File(llb.Rm("B")),
				}),
				contents: apply(
					fstest.CreateFile("A", []byte("A"), 0644),
				),
			},
			verifyContents{
				name:  "TestDiffAsParentMultipleLayers",
				state: llb.Diff(a(), abc()).File(llb.Mkfile("D", 0644, []byte("D"))),
				contents: apply(
					fstest.CreateFile("B", []byte("B"), 0644),
					fstest.CreateFile("C", []byte("C"), 0644),
					fstest.CreateFile("D", []byte("D"), 0644),
				),
			},
			verifyContents{
				name: "TestDiffAsParentMultipleLayerDelete",
				state: llb.Merge([]llb.State{
					ab(),
					llb.Diff(a(), abc()).File(llb.Rm("B")),
				}),
				contents: apply(
					fstest.CreateFile("A", []byte("A"), 0644),
					fstest.CreateFile("C", []byte("C"), 0644),
				),
			},
		}
	}()...)

	// Single layer diffs should reuse blobs
	tests = append(tests, func() []integration.Test {
		mergeBase := func() llb.State {
			return llb.Merge([]llb.State{
				alpine(),
				llb.Scratch().File(llb.Mkfile("/foo", 0644, []byte("/foo"))),
			})
		}
		return []integration.Test{
			verifyBlobReuse{
				name: "TestDiffSingleLayerBlobReuse",
				base: alpine(),
				upper: runShell(alpine(),
					"cat /dev/urandom | head -c 100 | sha256sum > /randomfile",
				),
			},
			verifyBlobReuse{
				name: "TestDiffSingleLayerOnMergeBlobReuse",
				base: mergeBase(),
				upper: runShell(mergeBase(),
					"cat /dev/urandom | head -c 100 | sha256sum > /randomfile",
				),
			},
		}
	}()...)

	// Regression tests
	tests = append(tests, func() []integration.Test {
		base := func() llb.State {
			return llb.Scratch().File(llb.Mkdir("/dir", 0755))
		}
		return []integration.Test{
			verifyContents{
				// Verifies that when a directory with contents is used as a base layer
				// in a merge, subsequent merges that first delete the dir (resulting in
				// a whiteout device w/ overlay snapshotters) and then recreate the dir
				// correctly set it as opaque.
				name: "TestDiffMergeOpaqueRegression",
				state: llb.Merge([]llb.State{
					base().File(llb.Mkfile("/dir/a", 0644, nil)),
					base().File(llb.Rm("/dir")),
					base().File(llb.Mkfile("/dir/b", 0644, nil)),
				}),
				contents: apply(
					fstest.CreateDir("/dir", 0755),
					fstest.CreateFile("/dir/b", nil, 0644),
				),
			},
			verifyContents{
				// Same as above, but with a file overwrite instead of an rm
				name: "TestDiffMergeOpaqueRegressionWithFileOverwrite",
				state: llb.Merge([]llb.State{
					base().File(llb.Mkfile("/dir/a", 0644, nil)),
					llb.Scratch().File(llb.Mkfile("/dir", 0644, nil)),
					base().File(llb.Mkfile("/dir/b", 0644, nil)),
				}),
				contents: apply(
					fstest.CreateDir("/dir", 0755),
					fstest.CreateFile("/dir/b", nil, 0644),
				),
			},
		}
	}()...)

	return tests
}

// contents lets you create fstest.Appliers using the sandbox, which
// enables i.e. using an llb.State or fstest.Applier interchangeably
// during test assertions on the contents of a given dir.
type contents func(sb integration.Sandbox) fstest.Applier

// implements fstest.Applier
type applyFn func(root string) error

func (a applyFn) Apply(root string) error {
	return a(root)
}

func contentsOf(state llb.State) contents {
	return func(sb integration.Sandbox) fstest.Applier {
		return applyFn(func(root string) error {
			ctx := sb.Context()
			c, err := New(ctx, sb.Address())
			if err != nil {
				return err
			}
			defer c.Close()

			def, err := state.Marshal(ctx)
			if err != nil {
				return err
			}

			_, err = c.Solve(ctx, def, SolveOpt{
				Exports: []ExportEntry{
					{
						Type:      ExporterLocal,
						OutputDir: root,
					},
				},
			}, nil)
			if err != nil {
				return err
			}
			return nil
		})
	}
}

func apply(appliers ...fstest.Applier) contents {
	return func(sb integration.Sandbox) fstest.Applier {
		return fstest.Apply(appliers...)
	}
}

func mergeContents(subContents ...contents) contents {
	return func(sb integration.Sandbox) fstest.Applier {
		var appliers []fstest.Applier
		for _, sub := range subContents {
			appliers = append(appliers, sub(sb))
		}
		return fstest.Apply(appliers...)
	}
}

func empty(sb integration.Sandbox) fstest.Applier {
	return applyFn(func(root string) error {
		return nil
	})
}

type verifyContents struct {
	name           string
	state          llb.State
	contents       contents
	skipOnRootless bool
}

func (tc verifyContents) Name() string {
	return tc.name
}

func (tc verifyContents) Run(t *testing.T, sb integration.Sandbox) {
	workers.CheckFeatureCompat(t, sb, workers.FeatureMergeDiff)
	if tc.skipOnRootless && sb.Rootless() {
		t.Skip("rootless")
	}

	switch tc.name {
	case "TestDiffUpperScratch":
		if workers.IsTestDockerdMoby(sb) {
			t.Skip("failed to handle changes: lstat ... no such file or directory: https://github.com/moby/buildkit/pull/2726#issuecomment-1070978499")
		}
	}

	requiresLinux(t)
	cdAddress := sb.ContainerdAddress()

	ctx := sb.Context()
	ctdCtx := namespaces.WithNamespace(ctx, "buildkit")

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	// verify the build has the expected contents
	imageName := fmt.Sprintf("buildkit/%s", strings.ToLower(tc.name))
	imageTarget := fmt.Sprintf("%s/%s:latest", registry, imageName)
	cacheName := fmt.Sprintf("buildkit/%s-cache", imageName)
	cacheTarget := fmt.Sprintf("%s/%s:latest", registry, cacheName)

	var importInlineCacheOpts []CacheOptionsEntry
	var exportInlineCacheOpts []CacheOptionsEntry
	var importRegistryCacheOpts []CacheOptionsEntry
	var exportRegistryCacheOpts []CacheOptionsEntry
	if !workers.IsTestDockerd() {
		importInlineCacheOpts = []CacheOptionsEntry{{
			Type: "registry",
			Attrs: map[string]string{
				"ref": imageTarget,
			},
		}}
		exportInlineCacheOpts = []CacheOptionsEntry{{
			Type: "inline",
		}}
		importRegistryCacheOpts = []CacheOptionsEntry{{
			Type: "registry",
			Attrs: map[string]string{
				"ref": cacheTarget,
			},
		}}
		exportRegistryCacheOpts = []CacheOptionsEntry{{
			Type: "registry",
			Attrs: map[string]string{
				"ref": cacheTarget,
			},
		}}
	}

	resetState(t, c, sb)
	requireContents(ctx, t, c, sb, tc.state, nil, exportInlineCacheOpts, imageTarget, tc.contents(sb))

	if workers.IsTestDockerd() {
		return
	}

	for _, importCacheOpts := range [][]CacheOptionsEntry{importInlineCacheOpts, importRegistryCacheOpts} {
		// Check that the cache is actually used. This can only be asserted on
		// in containerd-based tests because it needs access to the image+content store
		if cdAddress != "" {
			client, err := newContainerd(cdAddress)
			require.NoError(t, err)
			defer client.Close()

			def, err := tc.state.Marshal(sb.Context())
			require.NoError(t, err)

			resetState(t, c, sb)
			_, err = c.Solve(ctx, def, SolveOpt{
				Exports: []ExportEntry{
					{
						Type: ExporterImage,
						Attrs: map[string]string{
							"name":                                   imageTarget,
							"push":                                   "true",
							"unsafe-internal-store-allow-incomplete": "true",
						},
					},
				},
				CacheImports: importCacheOpts,
			}, nil)
			require.NoError(t, err)

			img, err := client.GetImage(ctdCtx, imageTarget)
			require.NoError(t, err)

			var unexpectedLayers []ocispecs.Descriptor
			require.NoError(t, images.Walk(ctdCtx, images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
				if images.IsLayerType(desc.MediaType) {
					_, err := client.ContentStore().Info(ctdCtx, desc.Digest)
					if err == nil {
						unexpectedLayers = append(unexpectedLayers, desc)
					} else {
						require.True(t, errdefs.IsNotFound(err))
					}
				}
				return images.Children(ctx, client.ContentStore(), desc)
			}), img.Target()))
			require.Empty(t, unexpectedLayers)
		}

		// verify that builds using cache reimport the same contents
		resetState(t, c, sb)
		requireContents(ctx, t, c, sb, tc.state, importCacheOpts, exportRegistryCacheOpts, imageTarget, tc.contents(sb))
	}
}

type verifyBlobReuse struct {
	name  string
	base  llb.State
	upper llb.State
}

func (tc verifyBlobReuse) Name() string {
	return tc.name
}

func (tc verifyBlobReuse) Run(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	cdAddress := sb.ContainerdAddress()
	if cdAddress == "" {
		t.Skip("test requires containerd worker")
	}

	client, err := newContainerd(cdAddress)
	require.NoError(t, err)
	defer client.Close()

	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	c, err := New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	// export the upper, find the layers that were created for it
	def, err := tc.upper.Marshal(sb.Context())
	require.NoError(t, err)

	imageName := fmt.Sprintf("buildkit/%s", strings.ToLower(tc.name))
	imageTarget := fmt.Sprintf("%s/%s:latest", registry, imageName)

	_, err = c.Solve(sb.Context(), def, SolveOpt{
		Exports: []ExportEntry{
			{
				Type: ExporterImage,
				Attrs: map[string]string{
					"name": imageTarget,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	img, err := client.GetImage(ctx, imageTarget)
	require.NoError(t, err)

	layerBlobs := map[digest.Digest]struct{}{}
	require.NoError(t, images.Walk(ctx, images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		if images.IsLayerType(desc.MediaType) {
			_, err := client.ContentStore().Info(ctx, desc.Digest)
			require.NoError(t, err)
			layerBlobs[desc.Digest] = struct{}{}
		}
		return images.Children(ctx, client.ContentStore(), desc)
	}), img.Target()))
	require.NotEmpty(t, layerBlobs)

	// export the diff, verify that no new layer blobs are created
	diff := llb.Diff(tc.base, tc.upper)
	def, err = diff.Marshal(sb.Context())
	require.NoError(t, err)

	_, err = c.Solve(sb.Context(), def, SolveOpt{
		Exports: []ExportEntry{
			{
				Type: ExporterImage,
				Attrs: map[string]string{
					"name": imageTarget,
					"push": "true",
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	img, err = client.GetImage(ctx, imageTarget)
	require.NoError(t, err)

	require.NoError(t, images.Walk(ctx, images.HandlerFunc(func(ctx context.Context, desc ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
		if images.IsLayerType(desc.MediaType) {
			_, err := client.ContentStore().Info(ctx, desc.Digest)
			require.NoError(t, err)
			if _, ok := layerBlobs[desc.Digest]; !ok {
				return nil, errors.Errorf("unexpected layer blob %s", desc.Digest)
			}
		}
		return images.Children(ctx, client.ContentStore(), desc)
	}), img.Target()))
}

func resetState(t *testing.T, c *Client, sb integration.Sandbox) {
	ctx := namespaces.WithNamespace(sb.Context(), "buildkit")
	cdAddress := sb.ContainerdAddress()
	if cdAddress != "" {
		client, err := newContainerd(cdAddress)
		require.NoError(t, err)
		defer client.Close()
		imageService := client.ImageService()
		imageList, err := imageService.List(ctx)
		require.NoError(t, err)
		for _, img := range imageList {
			err = imageService.Delete(ctx, img.Name, images.SynchronousDelete())
			require.NoError(t, err)
		}
	}
	checkAllReleasable(t, c, sb, true)
}
