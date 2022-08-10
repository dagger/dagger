package core

import (
	"testing"

	"github.com/dagger/cloak/core/schema/testutil"
	"github.com/stretchr/testify/require"
)

func TestCoreImage(t *testing.T) {
	res := struct {
		Core struct {
			Image struct {
				File string
			}
		}
	}{}

	testutil.Query(t,
		`{
			core {
				image(ref: "alpine:3.16.2") {
					file(path: "/etc/alpine-release")
				}
			}
		}`, nil, &res)

	require.Equal(t, res.Core.Image.File, "3.16.2\n")
}

func TestCoreGit(t *testing.T) {
	res := struct {
		Core struct {
			Git struct {
				File string
			}
		}
	}{}

	testutil.Query(t,
		`{
			core {
				git(remote: "github.com/dagger/dagger") {
					file(path: "README.md")
				}
			}
		}`, nil, &res)

	require.Contains(t, res.Core.Git.File, "dagger")
}
