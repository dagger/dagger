package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/testutil"
)

func TestDIND(t *testing.T) {
	t.Parallel()

	var res struct {
		Container struct {
			From struct {
				WithExec struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}

	err := testutil.Query(
		`
{
  container {
    from(address: "alpine") {
      withExec(args: ["apk", "add", "curl"]) {
        withExec(args: ["sh", "-c", """

mkdir /root/dir
touch /root/dir/1 /root/dir/2

curl \
-u $DAGGER_SESSION_TOKEN: \
-H "content-type:application/json" \
-d '{"query":"{host{directory(path:\"/root/dir\"){entries}}}"}' http://127.0.0.1:$DAGGER_SESSION_PORT/query
        """], experimentalPrivilegedNesting: true) {
          stdout
        }
        }
    }
  }
}


                `, &res, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Container.From.WithExec.WithExec.Stdout)
	require.Equal(t, "{\"data\":{\"host\":{\"directory\":{\"entries\":[\"1\",\"2\"]}}}}", res.Container.From.WithExec.WithExec.Stdout)
}
