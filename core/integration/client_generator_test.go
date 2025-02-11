package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ClientGeneratorTest struct{}

func TestClientGenerator(t *testing.T) {
	testctx.Run(testCtx, t, ClientGeneratorTest{}, Middleware()...)
}

func (ClientGeneratorTest) TestGenerateAndCallDependencies(ctx context.Context, t *testctx.T) {
	t.Run("use remote dependency", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		moduleSrc := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init")).
			With(daggerExec("install", "github.com/shykes/hello@2d789671a44c4d559be506a9bc4b71b0ba6e23c9")).
			WithExec([]string{"go", "mod", "init", "test.com/test"}).
			WithNewFile("main.go", `package main

import (
  "context"
  "fmt"

  "test.com/test/dagger"
)

func main() {
  ctx := context.Background()

  dag := dagger.Connect()

  res, err := dag.Hello().Hello(ctx)
  if err != nil {
    panic(err)
  }

  fmt.Println("result:", res)
}
`,
			).With(daggerClientAdd("go"))

		out, err := moduleSrc.With(daggerExec("run", "go", "run", "main.go")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "Hello from Dagger!\n", out)
	})
}
