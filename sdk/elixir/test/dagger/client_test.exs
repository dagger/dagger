defmodule Dagger.ClientTest do
  use ExUnit.Case, async: true

  alias Dagger.{
    BuildArg,
    Client,
    Container,
    Directory,
    EnvVariable,
    File,
    GitRef,
    GitRepository,
    Host,
    QueryError,
    Secret,
    Sync,
    ID
  }

  setup_all do
    client = Dagger.connect!(connect_timeout: :timer.seconds(60))
    on_exit(fn -> Dagger.close(client) end)

    %{client: client}
  end

  test "container", %{client: client} do
    assert {:ok, version} =
             client
             |> Client.container()
             |> Container.from("alpine:3.16.2")
             |> Container.with_exec(["cat", "/etc/alpine-release"])
             |> Container.stdout()

    assert version == "3.16.2\n"
  end

  test "git_repository", %{client: client} do
    assert {:ok, readme} =
             client
             |> Client.git("https://github.com/dagger/dagger")
             |> GitRepository.tag("v0.3.0")
             |> GitRef.tree()
             |> Directory.file("README.md")
             |> File.contents()

    assert ["## What is Dagger?" | _] = String.split(readme, "\n")
  end

  test "container build", %{client: client} do
    repo =
      client
      |> Client.git("https://github.com/dagger/dagger")
      |> GitRepository.tag("v0.3.0")
      |> GitRef.tree()

    assert {:ok, out} =
             client
             |> Client.container()
             |> Container.build(repo)
             |> Container.with_exec(["version"])
             |> Container.stdout()

    assert ["dagger" | _] = out |> String.trim() |> String.split(" ")
  end

  test "container build args", %{client: client} do
    dockerfile = """
    FROM alpine:3.16.2
    ARG SPAM=spam
    ENV SPAM=$SPAM
    CMD printenv
    """

    assert {:ok, out} =
             client
             |> Client.container()
             |> Container.build(
               client
               |> Client.directory()
               |> Directory.with_new_file("Dockerfile", dockerfile),
               build_args: [%BuildArg{name: "SPAM", value: "egg"}]
             )
             |> Container.stdout()

    assert out =~ "SPAM=egg"
  end

  test "container with env variable", %{client: client} do
    for val <- ["spam", ""] do
      assert {:ok, out} =
               client
               |> Client.container()
               |> Container.from("alpine:3.16.2")
               |> Container.with_env_variable("FOO", val)
               |> Container.with_exec(["sh", "-c", "echo -n $FOO"])
               |> Container.stdout()

      assert out == val
    end
  end

  test "container with mounted directory", %{client: client} do
    dir =
      client
      |> Client.directory()
      |> Directory.with_new_file("hello.txt", "Hello, world!")
      |> Directory.with_new_file("goodbye.txt", "Goodbye, world!")

    assert {:ok, out} =
             client
             |> Client.container()
             |> Container.from("alpine:3.16.2")
             |> Container.with_mounted_directory("/mnt", dir)
             |> Container.with_exec(["ls", "/mnt"])
             |> Container.stdout()

    assert out == """
           goodbye.txt
           hello.txt
           """
  end

  test "container with mounted cache", %{client: client} do
    cache_key = "example-cache"
    filename = DateTime.utc_now() |> Calendar.strftime("%Y-%m-%d-%H-%M-%S")

    container =
      client
      |> Client.container()
      |> Container.from("alpine:3.16.2")
      |> Container.with_mounted_cache("/cache", Client.cache_volume(client, cache_key))

    out =
      for i <- 1..5 do
        container
        |> Container.with_exec([
          "sh",
          "-c",
          "echo $0 >> /cache/#{filename}.txt; cat /cache/#{filename}.txt",
          to_string(i)
        ])
        |> Container.stdout()
      end

    assert [
             {:ok, "1\n"},
             {:ok, "1\n2\n"},
             {:ok, "1\n2\n3\n"},
             {:ok, "1\n2\n3\n4\n"},
             {:ok, "1\n2\n3\n4\n5\n"}
           ] = out
  end

  test "directory", %{client: client} do
    {:ok, entries} =
      client
      |> Client.directory()
      |> Directory.with_new_file("hello.txt", "Hello, world!")
      |> Directory.with_new_file("goodbye.txt", "Goodbye, world!")
      |> Directory.entries()

    assert entries == ["goodbye.txt", "hello.txt"]
  end

  test "host directory", %{client: client} do
    assert {:ok, readme} =
             client
             |> Client.host()
             |> Host.directory(".")
             |> Directory.file("README.md")
             |> File.contents()

    assert readme =~ "Dagger"
  end

  test "return list of objects", %{client: client} do
    assert {:ok, envs} =
             client
             |> Client.container()
             |> Container.from("alpine:3.16.2")
             |> Container.env_variables()

    assert is_list(envs)
    assert [{:ok, "PATH"}] = Enum.map(envs, &EnvVariable.name/1)
  end

  test "nullable", %{client: client} do
    assert {:ok, nil} =
             client
             |> Client.container()
             |> Container.from("alpine:3.16.2")
             |> Container.env_variable("NOTHING")
  end

  test "load file", %{client: client} do
    {:ok, id} =
      client
      |> Client.directory()
      |> Directory.with_new_file("hello.txt", "Hello, world!")
      |> Directory.file("hello.txt")
      |> File.id()

    assert {:ok, "Hello, world!"} =
             client
             |> Client.load_file_from_id(id)
             |> File.contents()
  end

  test "load secret", %{client: client} do
    {:ok, id} =
      client
      |> Client.set_secret("foo", "bar")
      |> Secret.id()

    assert {:ok, "bar"} =
             client
             |> Client.load_secret_from_id(id)
             |> Secret.plaintext()
  end

  test "container sync", %{client: client} do
    container =
      client
      |> Client.container()
      |> Container.from("alpine:3.16.2")

    assert {:error, %QueryError{}} =
             container |> Container.with_exec(["foobar"]) |> Sync.sync()

    assert {:ok, %Container{} = container} =
             container |> Container.with_exec(["echo", "spam"]) |> Sync.sync()

    assert {:ok, "spam\n"} = Container.stdout(container)
  end

  test "calling id before passing constructing arg" do
    dockerfile = """
    FROM alpine
    RUN --mount=type=secret,id=the-secret echo "hello ${THE_SECRET}"
    """

    client = Dagger.connect!()
    on_exit(fn -> Dagger.close(client) end)

    Elixir.File.write!("Dockerfile", dockerfile)
    on_exit(fn -> Elixir.File.rm_rf!("Dockerfile") end)

    secret =
      client
      |> Client.set_secret("the-secret", "abcd")

    assert {:ok, _} =
             client
             |> Client.host()
             |> Host.directory(".")
             |> Directory.docker_build(dockerfile: "Dockerfile", secrets: [secret])
             |> Sync.sync()

    container = Client.container(client)
    assert %Container{} = Client.container(client, id: container)
  end

  test "env variable expand", %{client: client} do
    {:ok, env} =
      client
      |> Client.container()
      |> Container.from("alpine:3.16.2")
      |> Container.with_env_variable("A", "B")
      |> Container.with_env_variable("A", "C:${A}", expand: true)
      |> Container.env_variable("A")

    assert env == "C:B"
  end

  test "service binding", %{client: client} do
    service =
      client
      |> Client.container()
      |> Container.from("nginx:1.25-alpine3.18")
      |> Container.with_exposed_port(80)
      |> Container.as_service()

    assert {:ok, out} =
             client
             |> Client.container()
             |> Container.from("alpine:3.18")
             |> Container.with_service_binding("nginx-service", service)
             |> Container.with_exec(~w"apk add curl")
             |> Container.with_exec(~w"curl http://nginx-service")
             |> Container.stdout()

    assert out =~ ~r/Welcome to nginx/
  end

  test "string escape", %{client: client} do
    assert {:ok, _} =
             client
             |> Client.container()
             |> Container.from("nginx:1.25-alpine3.18")
             |> Container.with_new_file(
               "/a.txt",
               """
                 \\  /       Partly cloudy
               _ /\"\".-.     +29(31) °C
                 \\_(   ).   ↑ 13 km/h
                 /(___(__)  10 km
                            0.0 mm
               """
             )
             |> Sync.sync()
  end
end
