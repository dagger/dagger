defmodule Dagger do
  @moduledoc """
  The [Dagger](https://dagger.io/) SDK for Elixir.

  ## Prerequisite

  The SDK depends on `docker` and `dagger` commands, please make sure those
  commands are presents on your `PATH`.

  ## Getting Started

  Let's try this script below

      Mix.install([:dagger])

      # 1
      Application.ensure_all_started(:inets)

      # 2
      {:ok, client} = Dagger.connect()

      # 3
      {:ok, output} =
        client
        |> Dagger.Client.container()
        |> Dagger.Container.from("hexpm/elixir:1.14.4-erlang-25.3-debian-buster-20230227-slim")
        |> Dagger.Container.with_exec(["elixir", "--version"])
        |> Dagger.Container.stdout()

      IO.puts(output)

      # 4
      Dagger.close(client)

  Here's what script do:

  1. Start `:inets` application in order to use `:httpc` as a HTTP client.
  2. Connecting to the Dagger Engine with `Dagger.connect/1`.
  3. Create a new container from `hexpm/elixir:1.14.4-erlang-25.3-debian-buster-20230227-slim`
     and calling a command `elixir` with flag `--version`, get the standard
     output from latest command and printing it to standard output.
  4. Close the connection.

  ## Accessing GraphQL API

  In case you want to execute GraphQL directly, the SDK provides `Dagger.Core.Client`,
  the client interface to the Dagger engine. The module provides 2 APIs for you:

  1. `Dagger.Core.Client.query/2` - to execute a GraphQL query to the Dagger engine.
  2. `Dagger.Core.Client.execute/2` - to execute a GraphQL query that produces by `Dagger.Core.QueryBuilder`.

  By default, every object types (`Dagger.Container`, `Dagger.Directory`, etc.) has
  a field `client` which is an instance of `Dagger.Core.Client`, you can use that instance
  from the object type without initialize connection by yourself.

  Please note that this API is an internal API, it may break from version to version. So
  please use with cautions.

  ## Module support

  The SDK also support Dagger Module, please see `Dagger.Mod.Object` for more
  details.
  """

  @doc """
  Connecting to Dagger.

  When calling this function, it try to connect in order:

  1. Use session from `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` shell
     environment variables.
  2. If (1) doesn't specified, it will lookup a binary defined in `_EXPERIMENTAL_DAGGER_CLI_BIN`
     and start a session.
  3. Download the latest binary from Dagger and start a session.

  ## Options

  #{NimbleOptions.docs(Dagger.Core.Client.connect_schema())}
  """
  def connect(opts \\ []) do
    with {:ok, engine_client} <- Dagger.Core.Client.connect(opts) do
      client = %Dagger.Client{
        client: engine_client,
        query_builder: Dagger.Core.QueryBuilder.query()
      }

      {:ok, client}
    end
  end

  @doc """
  Similar to `connect/1` but raise exception when found an error.
  """
  def connect!(opts \\ []) do
    case connect(opts) do
      {:ok, query} -> query
      error -> raise "Cannot connect to Dagger engine, cause: #{inspect(error)}"
    end
  end

  @doc """
  Connect to Dagger Engine and close connection automatically after `fun` executed.

  See `connect/1` for available options.
  """
  def with_connection(fun, opts \\ []) when is_function(fun, 1) and is_list(opts) do
    with {:ok, client} <- Dagger.connect(opts) do
      try do
        fun.(client)
      after
        close(client)
      end
    end
  end

  @doc """
  Disconnecting the client from Dagger Engine session.
  """
  def close(%Dagger.Client{client: client}) do
    Dagger.Core.Client.close(client)
  end
end
