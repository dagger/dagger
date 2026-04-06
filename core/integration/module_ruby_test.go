package core

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

// Group all tests that are specific to Ruby only.
type RubySuite struct{}

func TestRuby(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(RubySuite{})
}

func (RubySuite) TestInit(ctx context.Context, t *testctx.T) {
	t.Run("from scratch", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerInitRuby()).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("with different root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerInitRubyAt("child")).
			With(daggerCallAt("child", "container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("on develop", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerExec("init", "--source=.")).
			With(daggerExec("develop", "--sdk=ruby", "--source=.")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})
}

func (RubySuite) TestStringReturn(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := rubyModInit(t, c, `
require 'dagger'

class Test
  extend T::Sig
  extend Dagger::Module

  sig { params(name: String).returns(String) }
  def greeting(name:)
    "Hello, #{name}!"
  end
end
`).
		With(daggerCall("greeting", "--name", "World")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello, World!", out)
}

func (RubySuite) TestIntegerReturn(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := rubyModInit(t, c, `
require 'dagger'

class Test
  extend T::Sig
  extend Dagger::Module

  sig { params(a: Integer, b: Integer).returns(Integer) }
  def add(a:, b:)
    a + b
  end
end
`).
		With(daggerCall("add", "--a", "2", "--b", "3")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "5", out)
}

func (RubySuite) TestBooleanReturn(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := rubyModInit(t, c, `
require 'dagger'

class Test
  extend T::Sig
  extend Dagger::Module

  sig { params(value: T::Boolean).returns(T::Boolean) }
  def negate(value:)
    !value
  end
end
`).
		With(daggerCall("negate", "--value", "true")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "false", out)
}

func (RubySuite) TestContainerReturn(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := rubyModInit(t, c, `
require 'dagger'

class Test
  extend T::Sig
  extend Dagger::Module

  sig { params(msg: String).returns(Dagger::Container) }
  def echo_container(msg:)
    dag.container.from(address: "alpine:latest").with_exec(args: ["echo", "-n", msg])
  end
end
`).
		With(daggerCall("echo-container", "--msg", "hello from ruby", "stdout")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "hello from ruby", out)
}

func (RubySuite) TestMultipleFunctions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := rubyModInit(t, c, `
require 'dagger'

class Test
  extend T::Sig
  extend Dagger::Module

  sig { params(name: String).returns(String) }
  def hello(name:)
    "Hello, #{name}!"
  end

  sig { params(name: String).returns(String) }
  def goodbye(name:)
    "Goodbye, #{name}!"
  end
end
`)

	t.Run("hello", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCall("hello", "--name", "World")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, World!", out)
	})

	t.Run("goodbye", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCall("goodbye", "--name", "World")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Goodbye, World!", out)
	})
}

func (RubySuite) TestNoArgs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := rubyModInit(t, c, `
require 'dagger'

class Test
  extend T::Sig
  extend Dagger::Module

  sig { returns(String) }
  def hello
    "Hello, World!"
  end
end
`).
		With(daggerCall("hello")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello, World!", out)
}

// Helper to initialize a Ruby module with custom source
func rubyModInit(t *testctx.T, c *dagger.Client, source string) *dagger.Container {
	t.Helper()
	source = strings.TrimSpace(source)
	return daggerCliBase(t, c).
		With(daggerInitRuby()).
		With(rubySource(source))
}

func rubySource(contents string) dagger.WithContainerFunc {
	return fileContents("lib/test.rb", contents)
}

func daggerInitRuby(args ...string) dagger.WithContainerFunc {
	return daggerInitRubyAt("", args...)
}

func daggerInitRubyAt(modPath string, args ...string) dagger.WithContainerFunc {
	execArgs := append([]string{"init", "--sdk=ruby"}, args...)
	if len(args) == 0 {
		execArgs = append(execArgs, "--name=test")
	}
	if modPath != "" {
		execArgs = append(execArgs, "--source="+modPath, modPath)
	} else {
		execArgs = append(execArgs, "--source=.")
	}
	return daggerExec(execArgs...)
}
