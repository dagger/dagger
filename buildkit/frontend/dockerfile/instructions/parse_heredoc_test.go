package instructions

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types/strslice"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/stretchr/testify/require"
)

func TestErrorCasesHeredoc(t *testing.T) {
	cases := []struct {
		name          string
		dockerfile    string
		expectedError string
	}{
		{
			name:          "COPY heredoc destination",
			dockerfile:    "COPY /foo <<EOF\nEOF",
			expectedError: "COPY cannot accept a heredoc as a destination",
		},
	}
	for _, c := range cases {
		r := strings.NewReader(c.dockerfile)
		ast, err := parser.Parse(r)

		if err != nil {
			t.Fatalf("Error when parsing Dockerfile: %s", err)
		}
		n := ast.AST.Children[0]
		_, err = ParseInstruction(n)
		require.Error(t, err)
		require.Contains(t, err.Error(), c.expectedError)
	}
}

func TestCopyHeredoc(t *testing.T) {
	cases := []struct {
		dockerfile     string
		sourcesAndDest SourcesAndDest
	}{
		{
			dockerfile: "COPY /foo /bar",
			sourcesAndDest: SourcesAndDest{
				DestPath:    "/bar",
				SourcePaths: []string{"/foo"},
			},
		},
		{
			dockerfile: `COPY <<EOF /bar
EOF`,
			sourcesAndDest: SourcesAndDest{
				DestPath: "/bar",
				SourceContents: []SourceContent{
					{
						Path:   "EOF",
						Data:   "",
						Expand: true,
					},
				},
			},
		},
		{
			dockerfile: `COPY <<EOF /bar
TESTING
EOF`,
			sourcesAndDest: SourcesAndDest{
				DestPath: "/bar",
				SourceContents: []SourceContent{
					{
						Path:   "EOF",
						Data:   "TESTING\n",
						Expand: true,
					},
				},
			},
		},
		{
			dockerfile: `COPY <<-EOF /bar
	TESTING
EOF`,
			sourcesAndDest: SourcesAndDest{
				DestPath: "/bar",
				SourceContents: []SourceContent{
					{
						Path:   "EOF",
						Data:   "TESTING\n",
						Expand: true,
					},
				},
			},
		},
		{
			dockerfile: `COPY <<'EOF' /bar
TESTING
EOF`,
			sourcesAndDest: SourcesAndDest{
				DestPath: "/bar",
				SourceContents: []SourceContent{
					{
						Path:   "EOF",
						Data:   "TESTING\n",
						Expand: false,
					},
				},
			},
		},
		{
			dockerfile: `COPY <<EOF1 <<EOF2 /bar
this is the first file
EOF1
this is the second file
EOF2`,
			sourcesAndDest: SourcesAndDest{
				DestPath: "/bar",
				SourceContents: []SourceContent{
					{
						Path:   "EOF1",
						Data:   "this is the first file\n",
						Expand: true,
					},
					{
						Path:   "EOF2",
						Data:   "this is the second file\n",
						Expand: true,
					},
				},
			},
		},
		{
			dockerfile: `COPY <<EOF foo.txt /bar
this is inline
EOF`,
			sourcesAndDest: SourcesAndDest{
				DestPath:    "/bar",
				SourcePaths: []string{"foo.txt"},
				SourceContents: []SourceContent{
					{
						Path:   "EOF",
						Data:   "this is inline\n",
						Expand: true,
					},
				},
			},
		},
		{
			dockerfile: `COPY <<EOF /quotes
"quotes"
EOF`,
			sourcesAndDest: SourcesAndDest{
				DestPath: "/quotes",
				SourceContents: []SourceContent{
					{
						Path:   "EOF",
						Data:   "\"quotes\"\n",
						Expand: true,
					},
				},
			},
		},
	}

	for _, c := range cases {
		r := strings.NewReader(c.dockerfile)
		ast, err := parser.Parse(r)
		require.NoError(t, err)

		n := ast.AST.Children[0]
		comm, err := ParseInstruction(n)
		require.NoError(t, err)

		sd := comm.(*CopyCommand).SourcesAndDest
		require.Equal(t, c.sourcesAndDest, sd)
	}
}

func TestRunHeredoc(t *testing.T) {
	cases := []struct {
		dockerfile string
		shell      bool
		command    strslice.StrSlice
		files      []ShellInlineFile
	}{
		{
			dockerfile: `RUN ["ls", "/"]`,
			command:    strslice.StrSlice{"ls", "/"},
			shell:      false,
		},
		{
			dockerfile: `RUN ["<<EOF"]`,
			command:    strslice.StrSlice{"<<EOF"},
			shell:      false,
		},
		{
			dockerfile: "RUN ls /",
			command:    strslice.StrSlice{"ls /"},
			shell:      true,
		},
		{
			dockerfile: `RUN <<EOF
ls /
whoami
EOF`,
			command: strslice.StrSlice{"<<EOF"},
			files: []ShellInlineFile{
				{
					Name: "EOF",
					Data: "ls /\nwhoami\n",
				},
			},
			shell: true,
		},
		{
			dockerfile: `RUN <<'EOF' | python
print("hello")
print("world")
EOF`,
			command: strslice.StrSlice{"<<'EOF' | python"},
			files: []ShellInlineFile{
				{
					Name: "EOF",
					Data: `print("hello")
print("world")
`,
				},
			},
			shell: true,
		},
		{
			dockerfile: `RUN <<-EOF
	echo test
EOF`,
			command: strslice.StrSlice{"<<-EOF"},
			files: []ShellInlineFile{
				{
					Name:  "EOF",
					Data:  "\techo test\n",
					Chomp: true,
				},
			},
			shell: true,
		},
	}

	for _, c := range cases {
		r := strings.NewReader(c.dockerfile)
		ast, err := parser.Parse(r)
		require.NoError(t, err)

		n := ast.AST.Children[0]
		comm, err := ParseInstruction(n)
		require.NoError(t, err)
		require.Equal(t, c.shell, comm.(*RunCommand).PrependShell)
		require.Equal(t, c.command, comm.(*RunCommand).CmdLine)
		require.Equal(t, c.files, comm.(*RunCommand).Files)
	}
}
