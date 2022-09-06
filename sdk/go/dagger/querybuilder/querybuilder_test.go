package querybuilder

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryBuild(t *testing.T) {
	var contents string
	root := Query().
		Select("core").
		Select("image").Arg("ref", "alpine").
		Select("file").Arg("path", "/etc/alpine-release").Bind(&contents)

	q, err := root.Build()
	require.NoError(t, err)
	require.Equal(t, q, `query{core{image(ref:"alpine"){file(path:"/etc/alpine-release")}}}`)

	var response any
	err = json.Unmarshal([]byte(`
		{
			"core": {
				"image": {
				"file": "3.16.2\n"
				}
			}
		}
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.Unpack(response))
	require.Equal(t, "3.16.2\n", contents)
}

func TestQueryAlias(t *testing.T) {
	root := Query()
	alpine := root.
		Select("core").
		Select("image").Arg("ref", "alpine").
		Select("exec").Arg("args", []string{"apk", "add", "curl"}).
		Select("fs")

	var daggerStdout string
	alpine.
		SelectAs("dagger", "exec").Arg("args", []string{"curl", "https://dagger.io/"}).
		Select("stdout").Bind(&daggerStdout)

	var githubStdout string
	alpine.
		SelectAs("github", "exec").Arg("args", []string{"curl", "https://github.com/"}).
		Select("stdout").Bind(&githubStdout)

	q, err := root.Build()
	require.NoError(t, err)
	require.Equal(
		t,
		`query{core{image(ref:"alpine"){exec(args:["apk","add","curl"]){fs{dagger:exec(args:["curl","https://dagger.io/"]){stdout},github:exec(args:["curl","https://github.com/"]){stdout}}}}}}`,
		q,
	)

	var response any
	err = json.Unmarshal([]byte(`
		{
		  "core": {
			"image": {
			  "exec": {
				"fs": {
				  "dagger": {
					"stdout": "DAGGER STDOUT"
				  },
				  "github": {
					"stdout": "GITHUB STDOUT"
				  }
				}
			  }
			}
		  }
		}
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.Unpack(response))
	require.Equal(t, "DAGGER STDOUT", daggerStdout)
	require.Equal(t, "GITHUB STDOUT", githubStdout)

}

func TestImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, err := root.Select("a").Build()
	require.NoError(t, err)
	require.Equal(t, `query{test{a}}`, a)

	// Make sure this is not `test{a,b}` (e.g. the previous select didn't modify `root` in-place)
	b, err := root.Select("b").Build()
	require.NoError(t, err)
	require.Equal(t, `query{test{b}}`, b)
}
