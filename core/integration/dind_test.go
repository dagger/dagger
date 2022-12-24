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

touch /root/1 /root/2

curl \
-u $DAGGER_SESSION_TOKEN: \
-H "content-type:application/json" \
-d '{"query":"{host{directory(path:\"/root\"){entries}}}"}' $DAGGER_SESSION_URL/query
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
