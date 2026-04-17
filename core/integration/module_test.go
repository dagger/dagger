package core

// Workspace alignment: partially aligned; this historical umbrella suite still needs further cleanup.
// Scope: Remaining broad historical module coverage around runtime behavior and path/loading edge cases not yet split into narrower suites.
// Intent: Preserve confidence while incrementally extracting clearer module-owned suites out of the historical umbrella.
//
// Cleanup plan:
// 1. Done: exact-by-intent helpers live in module_helpers_test.go.
// 2. Done: legacy rewrite helpers live in module_legacy_helpers_test.go, visibly quarantined.
// 3. Done: workspace-owned command helpers live in workspace_test.go.
// 4. Next: peel additional coherent coverage slices out of this file without changing behavior.

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
)

type ModuleSuite struct{}

func TestModule(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleSuite{})
}

func (ModuleSuite) TestSecretNested(ctx context.Context, t *testctx.T) {
	t.Run("pass secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules

		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (_ *Secreter) Make() *dagger.Secret {
	return dag.SetSecret("FOO", "inner")
}

func (_ *Secreter) Get(ctx context.Context, secret *dagger.Secret) (string, error) {
	return secret.Plaintext(ctx)
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) TryReturn(ctx context.Context) error {
	text, err := dag.Secreter().Make().Plaintext(ctx)
	if err != nil {
		return err
	}
	if text != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", text)
	}
	return nil
}

func (t *Toplevel) TryArg(ctx context.Context) error {
	text, err := dag.Secreter().Get(ctx, dag.SetSecret("BAR", "outer"))
	if err != nil {
		return err
	}
	if text != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", text)
	}
	return nil
}
`,
			)

		t.Run("can pass secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{tryArg}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("can return secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{tryReturn}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("dockerfiles in modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("/input/Dockerfile", `FROM `+alpineImage+`
RUN --mount=type=secret,id=my-secret test "$(cat /run/secrets/my-secret)" = "barbar"
`).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (t *Test) Ctr(src *dagger.Directory) *dagger.Container {
	secret := dag.SetSecret("my-secret", "barbar")
	return src.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Secrets: []*dagger.Secret{secret},
		}).
		WithExec([]string{"true"}) // needed to avoid "no command set" error
}

func (t *Test) Evaluated(ctx context.Context, src *dagger.Directory) error {
	secret := dag.SetSecret("my-secret", "barbar")
	_, err := src.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Secrets: []*dagger.Secret{secret},
		}).
		WithExec([]string{"true"}).
		Sync(ctx)
	return err
}
`)

		_, err := ctr.
			With(daggerCall("ctr", "--src", "/input", "stdout")).
			Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.
			With(daggerCall("evaluated", "--src", "/input")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("pass embedded secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules when the secrets are embedded in containers rather than
		// passed directly

		t.Run("embedded in returns", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (*Dep) GetEncoded(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("FOO", "shhh")
	return dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
}

func (*Dep) GetCensored(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("BAR", "fdjsklajakldjfl")
	return dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return dag.Dep().GetEncoded().Stdout(ctx)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	return dag.Dep().GetCensored().Stdout(ctx)
}
`,
				)

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded in args", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (*Dep) Get(ctx context.Context, ctr *dagger.Container) (string, error) {
	return ctr.Stdout(ctx)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	secret := dag.SetSecret("FOO", "shhh")
	ctr := dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
	return dag.Dep().Get(ctx, ctr)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	secret := dag.SetSecret("BAR", "fdlaskfjdlsajfdkasl")
	ctr := dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
	return dag.Dep().Get(ctx, ctr)
}
`,
				)

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded through struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	Secret *dagger.Secret
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from foo"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", "cat /mnt/secret | tr [a-z] [A-Z]"}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("embedded through private struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	// +private
	Secret *dagger.Secret
	// +private
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from foo"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", "cat /mnt/secret | tr [a-z] [A-Z]"}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("double nested and called repeatedly", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			// Set up the base generator module
			ctr = ctr.
				WithWorkdir("/work/keychain/generator").
				With(daggerExec("init", "--name=generator-module", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
    "context"
    "dagger/generator-module/internal/dagger"
)

type GeneratorModule struct {
    // +private
    Password *dagger.Secret
}

func New() *GeneratorModule {
    return &GeneratorModule{
        Password: dag.SetSecret("pass", "admin"),
    }
}

func (m *GeneratorModule) Gen(ctx context.Context, name string) error {
    _, err := m.Password.Plaintext(ctx)
    return err
}
`)

			// Set up the keychain module that depends on generator
			ctr = ctr.
				WithWorkdir("/work/keychain").
				With(daggerExec("init", "--name=keychain", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./generator")).
				WithNewFile("main.go", `package main

import (
    "context"
)

type Keychain struct{}

func (m *Keychain) Get(ctx context.Context, name string) error {
    return dag.GeneratorModule().Gen(ctx, name)
}
`)

			// Set up the main module that uses keychain
			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=mymodule", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./keychain")).
				WithNewFile("main.go", `package main

import (
    "context"
    "fmt"
)

type Mymodule struct{}

func (m *Mymodule) Issue(ctx context.Context) error {
    kc := dag.Keychain()

    err := kc.Get(ctx, "a")
    if err != nil {
        return fmt.Errorf("first get: %w", err)
    }

    err = kc.Get(ctx, "a")
    if err != nil {
        return fmt.Errorf("second get, same args: %w", err)
    }

    err = kc.Get(ctx, "b")
    if err != nil {
        return fmt.Errorf("third get: %w", err)
    }
    return nil
}
`)

			// Test that repeated calls work correctly
			_, err := ctr.With(daggerCall("issue")).Sync(ctx)
			require.NoError(t, err)
		})

		t.Run("cached", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	Secret *dagger.Secret
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from mount"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
  "fmt"
)

type Test struct {}

func (m *Test) Foo(ctx context.Context) (string, error) {
  return m.impl(ctx, "foo")
}

func (m *Test) Bar(ctx context.Context) (string, error) {
  return m.impl(ctx, "bar")
}

func (m *Test) impl(ctx context.Context, name string) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", fmt.Sprintf("(echo %s && cat /mnt/secret) | tr [a-z] [A-Z]", name)}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerQuery("{foo,bar}")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo": "FOO\nHELLO FROM MOUNT", "bar": "BAR\nHELLO FROM MOUNT"}`, out)
		})
	})

	t.Run("parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	Ctr *dagger.Container
}

func (t *Test) FnA() *Test {
	secret := dag.SetSecret("FOO", "omg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) FnB(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("private parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	// +private
	Ctr *dagger.Container
}

func (t *Test) FnA() *Test {
	secret := dag.SetSecret("FOO", "omg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) FnB(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("parent field set in constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	Ctr *dagger.Container
}

func New() *Test {
	t := &Test{}
	secret := dag.SetSecret("FOO", "omfg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omfg\n", string(decoded))
	})

	t.Run("duplicate secret names", func(ctx context.Context, t *testctx.T) {
		// check that each module has it's own segmented secret store, by
		// writing secrets with the same name

		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/maker").
			With(daggerExec("init", "--name=maker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/maker/internal/dagger"
)

type Maker struct {}

func (_ *Maker) MakeSecret(ctx context.Context) (*dagger.Secret, error) {
	secret := dag.SetSecret("FOO", "inner")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return nil, err
	}
	return secret, nil
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./maker")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context) error {
	secret := dag.SetSecret("FOO", "outer")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return err
	}

	// this creates an inner secret "FOO", but it mustn't overwrite the outer one
	secret2 := dag.Maker().MakeSecret()

	plaintext, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", plaintext)
	}

	plaintext, err = secret2.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", plaintext)
	}

	return nil
}
`,
			)

		_, err := ctr.With(daggerQuery(`{attempt}`)).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secret by id leak", func(ctx context.Context, t *testctx.T) {
		// check that modules can't access each other's global secret stores,
		// even when we know the underlying IDs

		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/leaker").
			With(daggerExec("init", "--name=leaker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"

	"dagger/leaker/internal/dagger"
)

type Leaker struct {}

func (l *Leaker) Leak(ctx context.Context, target string) string {
	secret, _ := dag.LoadSecretFromID(dagger.SecretID(target)).Plaintext(ctx)
	return secret
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./leaker")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context, uniq string) error {
	secretID, err := dag.SetSecret("mysecret", "asdfasdf").ID(ctx)
	if err != nil {
		return err
	}

	// loading secret-by-id in the same module should succeed
	plaintext, err := dag.LoadSecretFromID(secretID).Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "asdfasdf" {
		return fmt.Errorf("expected \"asdfasdf\", but got %q", plaintext)
	}

	// but getting a leaker module to do this should fail
	plaintext, err = dag.Leaker().Leak(ctx, string(secretID))
	if err != nil {
		return err
	}
	if plaintext != "" {
		return fmt.Errorf("expected \"\", but got %q", plaintext)
	}

	return nil
}
`,
			)

		_, err := ctr.With(daggerQuery(`{attempt(uniq: %q)}`, identity.NewID())).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secrets cache normally", func(ctx context.Context, t *testctx.T) {
		// check that secrets cache as they would without nested modules,
		// which is essentially dependent on whether they have stable IDs

		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import "dagger/secreter/internal/dagger"

type Secreter struct {}

func (_ *Secreter) Make(uniq string) *dagger.Secret {
	return dag.SetSecret("MY_SECRET", uniq)
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", fmt.Sprintf(`package main

import (
	"context"
	"fmt"
	"dagger/toplevel/internal/dagger"
)

type Toplevel struct {}

func (_ *Toplevel) AttemptInternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.SetSecret("MY_SECRET", "foo"),
		dag.SetSecret("MY_SECRET", "bar"),
	)
}

func (_ *Toplevel) AttemptExternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.Secreter().Make("foo"),
		dag.Secreter().Make("bar"),
	)
}

func diffSecret(ctx context.Context, first, second *dagger.Secret) error {
	firstOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", first).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	secondOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", second).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	if firstOut != secondOut {
		return fmt.Errorf("%%q != %%q", firstOut, secondOut)
	}
	return nil
}
`, alpineImage),
			)

		t.Run("internal secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{attemptInternal}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("external secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{attemptExternal}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("optional secret field on module object", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pythonSource(`
import base64
import dagger
from dagger import dag, field, function, object_type


@object_type
class Test:
    @function
    def getobj(self, *, top_secret: dagger.Secret | None = None) -> "Obj":
        return Obj(top_secret=top_secret)


@object_type
class Obj:
    top_secret: dagger.Secret | None = field(default=None)

    @function
    async def getSecret(self) -> str:
        plaintext = await self.top_secret.plaintext()
        return base64.b64encode(plaintext.encode()).decode()
`)).
			With(daggerInitPython()).
			WithEnvVariable("TOP_SECRET", "omg").
			With(daggerCall("getobj", "--top-secret", "env://TOP_SECRET", "get-secret")).
			Stdout(ctx)

		require.NoError(t, err)
		decodeOut, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
		require.NoError(t, err)
		require.Equal(t, "omg", string(decodeOut))
	})
}

func (ModuleSuite) TestUnicodePath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/wórk/sub/").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/wórk/sub/main.go", `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Hello(ctx context.Context) string {
				return "hello"
 			}
 			`,
		).
		With(daggerQuery(`{hello}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"hello"}`, out)
}

func (ModuleSuite) TestStartServices(ctx context.Context, t *testctx.T) {
	// regression test for https://github.com/dagger/dagger/pull/6914
	t.Run("use service in multiple functions", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", fmt.Sprintf(`package main

	import (
		"context"
		"fmt"
		"dagger/test/internal/dagger"
	)

	type Test struct {
	}

	func (m *Test) FnA(ctx context.Context) (*Sub, error) {
		svc := dag.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				dag.Directory().WithNewFile("index.html", "hey there"),
			).
			WithWorkdir("/srv/www").
			WithExposedPort(23457).
			WithDefaultArgs([]string{"python", "-m", "http.server", "23457"}).
			AsService()

		ctr := dag.Container().
			From("%s").
			WithServiceBinding("svc", svc).
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"})

		out, err := ctr.Stdout(ctx)
		if err != nil {
			return nil, err
		}
		if out != "hey there" {
			return nil, fmt.Errorf("unexpected output: %%q", out)
		}
		return &Sub{Ctr: ctr}, nil
	}

	type Sub struct {
		Ctr *dagger.Container
	}

	func (m *Sub) FnB(ctx context.Context) (string, error) {
		return m.Ctr.
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"}).
			Stdout(ctx)
	}
	`, alpineImage),
			).
			With(daggerCall("fn-a", "fn-b")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey there", strings.TrimSpace(out))
	})

	// regression test for https://github.com/dagger/dagger/issues/6951
	t.Run("service in multiple containers", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", fmt.Sprintf(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (m *Test) Fn(ctx context.Context) *dagger.Container {
	redis := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	cli := dag.Container().
		From("redis").
		WithoutEntrypoint().
		WithServiceBinding("redis", redis)

	ctrA := cli.WithExec([]string{"sh", "-c", "redis-cli -h redis info >> /tmp/out.txt"})

	file := ctrA.Directory("/tmp").File("/out.txt")

	ctrB := dag.Container().
		From("%s").
		WithFile("/out.txt", file)

	return ctrB.WithExec([]string{"cat", "/out.txt"})
}
	`, alpineImage),
			).
			With(daggerCall("fn", "stdout")).
			Sync(ctx)
		require.NoError(t, err)
	})
}

func (ModuleSuite) TestReturnNilField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

type Test struct {
	A *Thing
	B *Thing
}

type Thing struct{}

func New() *Test {
	return &Test{
		A: &Thing{},
	}
}

func (m *Test) Hello() string {
	return "Hello"
}

`)).
		With(daggerCall("hello")).
		Sync(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestGetEmptyField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("without constructor", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
			With(sdkSource("go", `package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	D dagger.ImageLayerCompression
	E dagger.Platform
}

`)).
			With(daggerQuery("{a,b}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"a": "", "b": 0}`, out)
		// NOTE:
		// - trying to get C will try and decode an empty ID
		// - trying to get D will fail to instantiate an empty enum
		// - trying to get E will fail to parse the platform
		// ...but, we should be able to get the other values (important for backwards-compat)
	})

	t.Run("with constructor", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
			With(sdkSource("go", `package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	// these aren't tested here, since we can't give them zero values in the constructor
	// D dagger.ImageLayerCompression
	// E dagger.Platform
}

func New() *Test {
	return &Test{}
}
`)).
			With(daggerQuery("{a,b}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"a": "", "b": 0}`, out)
		// NOTE:
		// - trying to get C will try and decode an empty ID
		// ...but, we should be able to get the other values (important for backwards-compat)
	})
}

func (ModuleSuite) TestModulePreFilteringDirectory(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	t.Run("pre filtering directory on module call", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (t *Test) Call(
  // +ignore=[
  //   "foo.txt",
  //   "bar"
  // ]
  dir *dagger.Directory,
) *dagger.Directory {
 return dir
}`,
			},
			{
				sdk: "typescript",
				source: `import { object, func, Directory, argument } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  call(
    @argument({ ignore: ["foo.txt", "bar"] }) dir: Directory,
  ): Directory {
    return dir
  }
}`,
			},
			{
				sdk: "python",
				source: `from typing import Annotated

import dagger
from dagger import DefaultPath, Ignore, function, object_type


@object_type
class Test:
    @function
    async def call(
        self,
        dir: Annotated[dagger.Directory, Ignore(["foo.txt","bar"])],
    ) -> dagger.Directory:
        return dir
`,
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					// Add inputs
					WithDirectory("/work/input", c.
						Directory().
						WithNewFile("foo.txt", "foo").
						WithNewFile("bar.txt", "bar").
						WithDirectory("bar", c.Directory().WithNewFile("baz.txt", "baz"))).
					// Add dep
					WithWorkdir("/work/dep").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
					With(sdkSource(tc.sdk, tc.source)).
					// Setup test modules
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test-mod", "--sdk=go", "--source=.")).
					With(daggerExec("install", "./dep")).
					With(sdkSource("go", `package main

import (
	"dagger/test-mod/internal/dagger"
)

type TestMod struct {}

func (t *TestMod) Test(
  dir *dagger.Directory,
) *dagger.Directory {
 return dag.Test().Call(dir)
}`,
					))

				out, err := modGen.With(daggerCall("test", "--dir", "./input", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\n", out)
			})
		}
	})
}

func (ModuleSuite) TestFloat(ctx context.Context, t *testctx.T) {
	depSrc := `package main

type Dep struct{}

func (m *Dep) Dep(n float64) float32 {
	return float32(n)
}
`

	type testCase struct {
		sdk    string
		source string
	}

	testCases := []testCase{
		{
			sdk: "go",
			source: `package main

import "context"

type Test struct{}

func (m *Test) Test(n float64) float64 {
	return n
}

func (m *Test) TestFloat32(n float32) float32 {
	return n
}

func (m *Test) Dep(ctx context.Context, n float64) (float64, error) {
	return dag.Dep().Dep(ctx, n)
}`,
		},
		{
			sdk: "typescript",
			source: `import { dag, float, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  test(n: float): float {
    return n
  }

  @func()
  testFloat32(n: float): float {
    return n
  }

  @func()
  async dep(n: float): Promise<float> {
    return dag.dep().dep(n)
  }
}`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag

@dagger.object_type
class Test:
    @dagger.function
    def test(self, n: float) -> float:
        return n

    @dagger.function
    def testFloat32(self, n: float) -> float:
        return n

    @dagger.function
    async def dep(self, n: float) -> float:
        return await dag.dep().dep(n)
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("/work/dep/main.go", depSrc).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			t.Run("float64", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test", "--n=3.14")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `3.14`, out)
			})

			t.Run("float32", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-float-32", "--n=1.73424")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `1.73424`, out)
			})

			t.Run("call dep with float64 to float32 conversion", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("dep", "--n=232.3454")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `232.3454`, out)
			})
		})
	}
}

func (ModuleSuite) TestLoadWhenNoModule(ctx context.Context, t *testctx.T) {
	// verify that if a module is loaded from a directory w/ no module we don't
	// load extra files
	c := connect(ctx, t)

	tmpDir := t.TempDir()
	fileName := "foo"
	filePath := filepath.Join(tmpDir, fileName)
	require.NoError(t, os.WriteFile(filePath, []byte("foo"), 0o644))

	ents, err := c.ModuleSource(tmpDir).ContextDirectory().Entries(ctx)
	require.NoError(t, err)
	require.Empty(t, ents)
}

func (ModuleSuite) TestReturnNil(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {
	Dirs []*dagger.Directory
}

func (m *Test) Nothing() (*dagger.Directory, error) {
	return nil, nil
}

func (m *Test) ListWithNothing() ([]*dagger.Directory, error) {
	return []*dagger.Directory{nil}, nil
}

func (m *Test) ObjsWithNothing() ([]*Test, error) {
	return []*Test{
		nil,
		{
			Dirs: []*dagger.Directory{nil},
		},
	}, nil
}
`,
		)

	out, err := modGen.With(daggerQuery(`{nothing{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"nothing":null}`, out)

	out, err = modGen.With(daggerQuery(`{listWithNothing{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"listWithNothing":[null]}`, out)

	out, err = modGen.With(daggerQuery(`{objsWithNothing{dirs{entries}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"objsWithNothing":[null,{"dirs":[null]}]}`, out)
}

func (ModuleSuite) TestFunctionCacheControl(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk    string
		source string
	}{
		{
			// TODO: add test that function doc strings still get parsed correctly, don't include //+ etc.
			sdk: "go",
			source: `package main

import (
	"crypto/rand"
)

type Test struct{}

// My cool doc on TestTtl
// +cache="40s"
func (m *Test) TestTtl() string {
	return rand.Text()
}

// My dope doc on TestCachePerSession
// +cache="session"
func (m *Test) TestCachePerSession() string {
	return rand.Text()
}

// My darling doc on TestNeverCache
// +cache="never"
func (m *Test) TestNeverCache() string {
	return rand.Text()
}

// My rad doc on TestAlwaysCache
func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
import random
import string

@dagger.object_type
class Test:
		@dagger.function(cache="40s")
		def test_ttl(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function(cache="session")
		def test_cache_per_session(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function(cache="never")
		def test_never_cache(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function
		def test_always_cache(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))
`,
		},

		{
			sdk: "typescript",
			source: `
import crypto from "crypto"

import {  object, func } from "@dagger.io/dagger"

@object()
export class Test {
	@func({ cache: "40s"})
	testTtl(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func({ cache: "session" })
	testCachePerSession(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func({ cache: "never" })
	testNeverCache(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func()
	testAlwaysCache(): string {
		return crypto.randomBytes(16).toString("hex")
	}
}

`,
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			t.Run("always cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				// TODO: this is gonna be flaky to cache prunes, might need an isolated engine

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()). // don't cache the nested execs themselves
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)

				require.Equal(t, out1, out2, "outputs should be equal since the result is always cached")
			})

			t.Run("cache per session", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out1a, out1b, "outputs should be equal since they are from the same session")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out2a, out2b, "outputs should be equal since they are from the same session")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are from different sessions")
			})

			t.Run("never cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1a, out1b, "outputs should not be equal since they are never cached")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out2a, out2b, "outputs should not be equal since they are never cached")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are never cached")
			})

			// TODO: this is gonna be hella flaky probably, need isolated engine to combat pruning and probably more generous times...
			t.Run("cache ttl", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c2.Close())

				require.Equal(t, out1, out2, "outputs should be equal since the cache ttl has not expired")
				time.Sleep(41 * time.Second)

				c3 := connect(ctx, t)
				modGen3 := modInit(t, c3, tc.sdk, tc.source)

				out3, err := modGen3.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1, out3, "outputs should not be equal since the cache ttl has expired")
			})
		})
	}

	// rest of tests are SDK agnostic so just test w/ go
	t.Run("setSecret invalidates cache", func(ctx context.Context, t *testctx.T) {
		const modSDK = "go"
		const modSrc = `package main

import (
	"crypto/rand"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestSetSecret() *dagger.Container {
	r := rand.Text()
	s := dag.SetSecret(r, r)
	return dag.Container().
		From("` + alpineImage + `").
		WithSecretVariable("TOP_SECRET", s)
}
`

		// in memory cache should be hit within a session, but
		// no cache hits across sessions should happen

		c1 := connect(ctx, t)
		modGen1 := modInit(t, c1, modSDK, modSrc)

		out1a, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out1b, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out1a, out1b)
		require.NoError(t, c1.Close())

		c2 := connect(ctx, t)
		modGen2 := modInit(t, c2, modSDK, modSrc)

		out2a, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out2b, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out2a, out2b)

		require.NotEqual(t, out1a, out2a)
	})

	t.Run("dependency contextual arg", func(ctx context.Context, t *testctx.T) {
		const modSDK = "go"
		const modSrc = `package main
import (
	"context"
	"dagger/test/internal/dagger"
)
type Test struct{}
func (m *Test) CallDep(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().Test().Sync(ctx)
}
func (m *Test) CallDepFile(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().TestFile().Sync(ctx)
}
`

		const depSrc = `package main
import (
	"dagger/dep/internal/dagger"
)
type Dep struct{}
func (m *Dep) Test() *dagger.Directory {
	return dag.Depdep().Test()
}
func (m *Dep) TestFile() *dagger.Directory {
	return dag.Depdep().TestFile()
}
`

		const depDepSrc = `package main
import (
	"crypto/rand"
	"dagger/depdep/internal/dagger"
)
type Depdep struct{}
func (m *Depdep) Test(
	// +defaultPath="."
	dir *dagger.Directory,
) *dagger.Directory {
	return dir.WithNewFile("rand.txt", rand.Text())
}
func (m *Depdep) TestFile(
	// +defaultPath="dagger.json"
	f *dagger.File,
) *dagger.Directory {
	return dag.Directory().
		WithFile("dagger.json", f).
		WithNewFile("rand.txt", rand.Text())
}
`

		getModGen := func(c *dagger.Client) *dagger.Container {
			return goGitBase(t, c).
				WithWorkdir("/work/depdep").
				With(daggerExec("init", "--name=depdep", "--sdk="+modSDK, "--source=.")).
				WithNewFile("/work/depdep/main.go", depDepSrc).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk="+modSDK, "--source=.")).
				With(daggerExec("install", "../depdep")).
				WithNewFile("/work/dep/main.go", depSrc).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+modSDK, "--source=.")).
				With(sdkSource(modSDK, modSrc)).
				With(daggerExec("install", "./dep"))
		}

		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})
	})

	t.Run("git contextual arg", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()

		// Initialize git repo
		gitCmd := exec.Command("git", "init")
		gitCmd.Dir = modDir
		gitOutput, err := gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "config", "user.email", "dagger@example.com")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "config", "user.name", "Dagger Tests")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		// Initialize dagger module
		initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--name=test", "--sdk=go", "--source=.")
		initOutput, err := initCmd.CombinedOutput()
		require.NoError(t, err, string(initOutput))

		installCmd := hostDaggerCommand(ctx, t, modDir, "install",
			"github.com/dagger/dagger-test-modules/contextual-git-bug@"+vcsTestCaseCommit)
		installOutput, err := installCmd.CombinedOutput()
		require.NoError(t, err, string(installOutput))

		// Write module source
		err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
    "context"
    "dagger/test/internal/dagger"
)

type Test struct {
    //+private
    Ref *dagger.GitRef
    //+private
    Dep *dagger.Dep
}

func New(
    // +defaultPath="."
    ref *dagger.GitRef,
    //+defaultPath="crap"
    source *dagger.Directory,
) *Test {
    return &Test{
        Ref: ref,
        Dep: dag.Dep(source),
    }
}

func (m *Test) Fn(
    ctx context.Context,
    //+defaultPath="config/config.local.js"
    configFile *dagger.File,
) (*dagger.Directory, error) {
    return m.Dep.WithRef(m.Ref).Fn().WithFile("config.js", configFile).Sync(ctx)
}
`), 0644)
		require.NoError(t, err)

		// Create git commit
		gitCmd = exec.Command("git", "add", ".")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "commit", "-m", "make HEAD exist")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		// Create directories and config file
		require.NoError(t, os.MkdirAll(filepath.Join(modDir, "crap"), 0755))
		require.NoError(t, os.MkdirAll(filepath.Join(modDir, "config"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(modDir, "config", "config.local.js"), []byte("1"), 0644))

		// Run first dagger call
		callCmd := hostDaggerCommand(ctx, t, modDir, "call", "fn")
		callOutput, err := callCmd.CombinedOutput()
		require.NoError(t, err, string(callOutput))

		// Update config file
		require.NoError(t, os.WriteFile(filepath.Join(modDir, "config", "config.local.js"), []byte("2"), 0644))

		// Run second dagger call
		callCmd = hostDaggerCommand(ctx, t, modDir, "call", "fn")
		callOutput, err = callCmd.CombinedOutput()
		require.NoError(t, err, string(callOutput))
	})
}
