defmodule Dagger.ClientTest do
  use ExUnit.Case, async: true

  alias Dagger.{
    BuildArg,
    CacheSharingMode,
    Client,
    Container,
    Directory,
    EnvVariable,
    File,
    GitRef,
    GitRepository,
    Host,
    Secret,
    Sync
  }

  alias Dagger.Core.ExecError

  setup_all do
    dag = Dagger.connect!(connect_timeout: :timer.seconds(60))
    on_exit(fn -> Dagger.close(dag) end)

    %{dag: dag}
  end

  test "container", %{dag: dag} do
    assert {:ok, version} =
             dag
             |> Client.container()
             |> Container.from("alpine:3.20.2")
             |> Container.with_exec(["cat", "/etc/alpine-release"])
             |> Container.stdout()

    assert version == "3.20.2\n"
  end

  test "git_repository", %{dag: dag} do
    assert {:ok, readme} =
             dag
             |> Client.git("https://github.com/dagger/dagger")
             |> GitRepository.tag("v0.3.0")
             |> GitRef.tree()
             |> Directory.file("README.md")
             |> File.contents()

    assert ["## What is Dagger?" | _] = String.split(readme, "\n")
  end

  test "container build", %{dag: dag} do
    repo =
      dag
      |> Client.git("https://github.com/dagger/dagger")
      |> GitRepository.tag("v0.3.0")
      |> GitRef.tree()

    assert {:ok, out} =
             repo
             |> Directory.docker_build()
             |> Container.with_exec(["dagger", "version"])
             |> Container.stdout()

    assert ["dagger" | _] = out |> String.trim() |> String.split(" ")
  end

  test "container build args", %{dag: dag} do
    dockerfile = """
    FROM alpine:3.20.2
    ARG SPAM=spam
    ENV SPAM=$SPAM
    CMD printenv
    """

    assert {:ok, out} =
             dag
             |> Client.directory()
             |> Directory.with_new_file("Dockerfile", dockerfile)
             |> Directory.docker_build(build_args: [%BuildArg{name: "SPAM", value: "egg"}])
             |> Container.with_exec([])
             |> Container.stdout()

    assert out =~ "SPAM=egg"
  end

  test "container with env variable", %{dag: dag} do
    for val <- ["spam", ""] do
      assert {:ok, out} =
               dag
               |> Client.container()
               |> Container.from("alpine:3.20.2")
               |> Container.with_env_variable("FOO", val)
               |> Container.with_exec(["sh", "-c", "echo -n $FOO"])
               |> Container.stdout()

      assert out == val
    end
  end

  test "container with mounted directory", %{dag: dag} do
    dir =
      dag
      |> Client.directory()
      |> Directory.with_new_file("hello.txt", "Hello, world!")
      |> Directory.with_new_file("goodbye.txt", "Goodbye, world!")

    assert {:ok, out} =
             dag
             |> Client.container()
             |> Container.from("alpine:3.20.2")
             |> Container.with_mounted_directory("/mnt", dir)
             |> Container.with_exec(["ls", "/mnt"])
             |> Container.stdout()

    assert out == """
           goodbye.txt
           hello.txt
           """
  end

  test "container with mounted cache", %{dag: dag} do
    cache_key = "example-cache"
    filename = DateTime.utc_now() |> Calendar.strftime("%Y-%m-%d-%H-%M-%S")

    container =
      dag
      |> Client.container()
      |> Container.from("alpine:3.20.2")
      |> Container.with_mounted_cache("/cache", Client.cache_volume(dag, cache_key),
        sharing: CacheSharingMode.locked()
      )

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

  test "directory", %{dag: dag} do
    {:ok, entries} =
      dag
      |> Client.directory()
      |> Directory.with_new_file("hello.txt", "Hello, world!")
      |> Directory.with_new_file("goodbye.txt", "Goodbye, world!")
      |> Directory.entries()

    assert entries == ["goodbye.txt", "hello.txt"]
  end

  test "host directory", %{dag: dag} do
    assert {:ok, readme} =
             dag
             |> Client.host()
             |> Host.directory(".")
             |> Directory.file("README.md")
             |> File.contents()

    assert readme =~ "Dagger"
  end

  test "return list of objects", %{dag: dag} do
    assert {:ok, envs} =
             dag
             |> Client.container()
             |> Container.from("alpine:3.20.2")
             |> Container.env_variables()

    assert is_list(envs)
    assert [{:ok, "PATH"}] = Enum.map(envs, &EnvVariable.name/1)
  end

  test "nullable", %{dag: dag} do
    assert {:ok, nil} =
             dag
             |> Client.container()
             |> Container.from("alpine:3.20.2")
             |> Container.env_variable("NOTHING")
  end

  test "load file", %{dag: dag} do
    {:ok, id} =
      dag
      |> Client.directory()
      |> Directory.with_new_file("hello.txt", "Hello, world!")
      |> Directory.file("hello.txt")
      |> File.id()

    assert {:ok, "Hello, world!"} =
             dag
             |> Client.load_file_from_id(id)
             |> File.contents()
  end

  test "load secret", %{dag: dag} do
    {:ok, id} =
      dag
      |> Client.set_secret("foo", "bar")
      |> Secret.id()

    assert {:ok, "bar"} =
             dag
             |> Client.load_secret_from_id(id)
             |> Secret.plaintext()
  end

  test "container sync", %{dag: dag} do
    container =
      dag
      |> Client.container()
      |> Container.from("alpine:3.20.2")

    assert {:error, %ExecError{}} =
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

    dag = Dagger.connect!()
    on_exit(fn -> Dagger.close(dag) end)

    Elixir.File.write!("Dockerfile", dockerfile)
    on_exit(fn -> Elixir.File.rm_rf!("Dockerfile") end)

    secret =
      dag
      |> Client.set_secret("the-secret", "abcd")

    assert {:ok, _} =
             dag
             |> Client.host()
             |> Host.directory(".")
             |> Directory.docker_build(dockerfile: "Dockerfile", secrets: [secret])
             |> Sync.sync()

    container = Client.container(dag)
    assert %Container{} = Client.container(dag, id: container)
  end

  test "env variable expand", %{dag: dag} do
    {:ok, env} =
      dag
      |> Client.container()
      |> Container.from("alpine:3.20.2")
      |> Container.with_env_variable("A", "B")
      |> Container.with_env_variable("A", "C:${A}", expand: true)
      |> Container.env_variable("A")

    assert env == "C:B"
  end

  test "service binding", %{dag: dag} do
    service =
      dag
      |> Client.container()
      |> Container.from("nginx:1.25-alpine3.18")
      |> Container.with_exposed_port(80)
      |> Container.as_service(use_entrypoint: true)

    assert {:ok, out} =
             dag
             |> Client.container()
             |> Container.from("alpine:3.18")
             |> Container.with_service_binding("nginx-service", service)
             |> Container.with_exec(~w"apk add curl")
             |> Container.with_exec(~w"curl http://nginx-service")
             |> Container.stdout()

    assert out =~ ~r/Welcome to nginx/
  end

  test "string escape", %{dag: dag} do
    assert {:ok, _} =
             dag
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

  test "return scalar", %{dag: dag} do
    assert {:ok, :OBJECT_KIND} =
             dag
             |> Dagger.Client.type_def()
             |> Dagger.TypeDef.with_object("A")
             |> Dagger.TypeDef.kind()
  end

  test "exec error", %{dag: dag} do
    assert {:error, error} =
             dag
             |> Client.container()
             |> Container.from("alpine:3.20.2")
             |> Container.with_exec(["foobar"])
             |> Sync.sync()

    assert Exception.message(error) ==
             "input: container.from.withExec.sync process \"foobar\" did not complete successfully: exit code: 1"
  end

  test "iss 8601 - Dagger.Directory.with_directory/4 should not crash", %{dag: dag} do
    dir =
      dag
      |> Client.directory()
      |> Directory.with_new_directory("/abcd")

    assert {:ok, entries} =
             dag
             |> Client.directory()
             |> Directory.with_directory("/", dir)
             |> Directory.entries()

    assert entries == ["abcd/"]
  end
end
