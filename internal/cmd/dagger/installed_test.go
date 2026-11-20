package daggercmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleListCommand(t *testing.T) {
	require.Equal(t, "List installed modules", installedCmd.Short)
}

func TestPrintModuleList(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printModuleList(&out, []moduleListRow{
		{Name: "foo", Source: "./foo"},
		{Name: "bar", Source: "github.com/example/bar@v1.0.0"},
	}))
	require.Equal(t, "NAME  SOURCE\nfoo   ./foo\nbar   github.com/example/bar@v1.0.0\n", out.String())
}

func TestPrintEmptyModuleList(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, printModuleList(&out, nil))
	require.Empty(t, out.String())
}

func TestFormatModuleTree(t *testing.T) {
	require.Equal(t, `├── app (./app)
│   ├── dependency lib (./lib)
│   │   └── dependency shared (./shared)
│   └── client other (./other) (*)
├── other (./other)
│   └── dependency shared (./shared) (*)
├── broken (./broken) (error: no module here)
└── scope tools (./tools)
    └── client app (./app) (*)
`, formatModuleTree([]*moduleTreeNode{
		{Label: "app (./app)", Children: []*moduleTreeNode{
			{Label: "dependency lib (./lib)", Children: []*moduleTreeNode{
				{Label: "dependency shared (./shared)"},
			}},
			{Label: "client other (./other) (*)"},
		}},
		{Label: "other (./other)", Children: []*moduleTreeNode{
			{Label: "dependency shared (./shared) (*)"},
		}},
		{Label: "broken (./broken) (error: no module here)"},
		{Label: "scope tools (./tools)", Children: []*moduleTreeNode{
			{Label: "client app (./app) (*)"},
		}},
	}, ""))
}

func TestModuleTreeKey(t *testing.T) {
	require.Equal(t, "./app", moduleTreeKey("app"))
	require.Equal(t, "./app", moduleTreeKey("./app/"))
	require.Equal(t, "../lib", moduleTreeKey("../lib"))
	require.Equal(t, ".", moduleTreeKey("."))
	require.Equal(t, "github.com/example/lib@v1.0.0", moduleTreeKey("github.com/example/lib@v1.0.0"))
}
