package core

import (
	"testing"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDIND(t *testing.T) {
	t.Parallel()

	var res struct {
		Container struct {
			From struct {
				WithWorkDir struct {
					WithExec struct {
						WithNewFile struct {
							WithExec struct {
								Stdout string
							}
						}
					}
				}
			}
		}
	}

	err := testutil.Query(
		`
{
  container {
    from(address: "golang") {
      withWorkdir(path: "/usr/src/app") {
        withExec(args: ["sh", "-c", "go mod init test && go get dagger.io/dagger@main"]) {
          withNewFile(
            contents: """
package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	entries, err := client.Host().Directory(".").Entries(ctx)
	if err != nil {
		panic(err)
	}

	// print output to console
	fmt.Println(entries)
}

            """
            path: "main.go"
          ) {
            withExec(args: ["go", "run", "main.go"], experimentalPrivilegedNesting: true) {
              stdout
            }
          }
        }
      }
    }
  }
}

                `, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Container.From.WithWorkDir.WithExec.WithNewFile.WithExec.Stdout)
	require.Equal(t, "[go.mod go.sum main.go]\n", res.Container.From.WithWorkDir.WithExec.WithNewFile.WithExec.Stdout)
}
