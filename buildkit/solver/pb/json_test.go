package pb

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOp_UnmarshalJSON(t *testing.T) {
	for _, tt := range []struct {
		name string
		op   *Op
	}{
		{
			name: "exec",
			op: &Op{
				Op: &Op_Exec{
					Exec: &ExecOp{
						Meta: &Meta{
							Args: []string{"echo", "Hello", "World"},
						},
						Mounts: []*Mount{
							{Input: 0, Dest: "/", Readonly: true},
						},
					},
				},
			},
		},
		{
			name: "source",
			op: &Op{
				Op: &Op_Source{
					Source: &SourceOp{
						Identifier: "local://context",
					},
				},
				Constraints: &WorkerConstraints{},
			},
		},
		{
			name: "file",
			op: &Op{
				Op: &Op_File{
					File: &FileOp{
						Actions: []*FileAction{
							{
								Input:  1,
								Output: 2,
								Action: &FileAction_Copy{
									Copy: &FileActionCopy{
										Src:  "/foo",
										Dest: "/bar",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "build",
			op: &Op{
				Op: &Op_Build{
					Build: &BuildOp{
						Def: &Definition{},
					},
				},
			},
		},
		{
			name: "merge",
			op: &Op{
				Op: &Op_Merge{
					Merge: &MergeOp{
						Inputs: []*MergeInput{
							{Input: 0},
							{Input: 1},
						},
					},
				},
			},
		},
		{
			name: "diff",
			op: &Op{
				Op: &Op_Diff{
					Diff: &DiffOp{
						Lower: &LowerDiffInput{Input: 0},
						Upper: &UpperDiffInput{Input: 1},
					},
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := json.Marshal(tt.op)
			if err != nil {
				t.Fatal(err)
			}

			exp, got := tt.op, &Op{}
			if err := json.Unmarshal(out, got); err != nil {
				t.Fatal(err)
			}
			require.Equal(t, exp, got)
		})
	}
}

func TestFileAction_UnmarshalJSON(t *testing.T) {
	for _, tt := range []struct {
		name       string
		fileAction *FileAction
	}{
		{
			name: "copy",
			fileAction: &FileAction{
				Action: &FileAction_Copy{
					Copy: &FileActionCopy{
						Src:  "/foo",
						Dest: "/bar",
					},
				},
			},
		},
		{
			name: "mkfile",
			fileAction: &FileAction{
				Action: &FileAction_Mkfile{
					Mkfile: &FileActionMkFile{
						Path: "/foo",
						Data: []byte("Hello, World!"),
					},
				},
			},
		},
		{
			name: "mkdir",
			fileAction: &FileAction{
				Action: &FileAction_Mkdir{
					Mkdir: &FileActionMkDir{
						Path:        "/foo/bar",
						MakeParents: true,
					},
				},
			},
		},
		{
			name: "rm",
			fileAction: &FileAction{
				Action: &FileAction_Rm{
					Rm: &FileActionRm{
						Path:          "/foo",
						AllowNotFound: true,
					},
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := json.Marshal(tt.fileAction)
			if err != nil {
				t.Fatal(err)
			}

			exp, got := tt.fileAction, &FileAction{}
			if err := json.Unmarshal(out, got); err != nil {
				t.Fatal(err)
			}
			require.Equal(t, exp, got)
		})
	}
}

func TestUserOpt_UnmarshalJSON(t *testing.T) {
	for _, tt := range []struct {
		name    string
		userOpt *UserOpt
	}{
		{
			name: "byName",
			userOpt: &UserOpt{
				User: &UserOpt_ByName{
					ByName: &NamedUserOpt{
						Name: "foo",
					},
				},
			},
		},
		{
			name: "byId",
			userOpt: &UserOpt{
				User: &UserOpt_ByID{
					ByID: 2,
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := json.Marshal(tt.userOpt)
			if err != nil {
				t.Fatal(err)
			}

			exp, got := tt.userOpt, &UserOpt{}
			if err := json.Unmarshal(out, got); err != nil {
				t.Fatal(err)
			}
			require.Equal(t, exp, got)
		})
	}
}
