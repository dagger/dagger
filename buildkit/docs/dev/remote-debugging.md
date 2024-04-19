# Remote Debugging

Remote debugging allows a developer to run buildkit in a container while running the debugger through their local IDE.
In Go, remote debugging is supported through [delve](https://github.com/go-delve/delve) and can be accessed either
through the command line or through certain IDEs (like [JetBrains GoLand](https://www.jetbrains.com/go/)).

This method of debugging is in contrast to some other methods employed by IDEs such as VS Code. In VS Code, it's common
to create a devcontainer which runs the IDE itself inside a container. This means the compiled application isn't running
in its own docker container, but the container that has been configured by VS Code.

## Building the buildkit image with the debug variant

The buildkit image can be created with the debug variant by setting the build argument `BUILDKIT_DEBUG=1`.

```bash
$ BUILDKIT_DEBUG=1 make images
```

## Running the buildkit image

Delve runs on port 5000 but a different host address can be used if there's a port conflict. It's also recommended that
you limit the host port to the loopback interface (localhost) to prevent exposing delve to external computers.

```bash
$ docker run --privileged -d --name=buildkit-dev \
    -p 127.0.0.1:5000:5000 \
    --restart always \
    moby/buildkit:local
```

It's also useful to use `--restart always` when debugging. Delve will always shutdown the program with `SIGTERM` when
the last client disconnects even when headless and multiclient mode are enabled. That's just how the program works
and it can be a bit annoying to restart the program repeatedly.

If no debugging client connects to the debug image, it should work identically to the release image just slower because
it's running through the debugger without optimizations.

## Adding this container to docker buildx

If using `docker` to run builds, you can inform docker about this new instance of buildkit by running the following:

```bash
$ docker buildx create --name=dev --driver=remote docker-container://buildkit-dev
```

You can then set the builder in one of the following ways:

```bash
# Through environment variable (easiest and can be exported to the shell).
$ BUILDX_BUILDER=dev docker buildx build ...
# Through command line option (easy but can't be set for the entire shell).
$ docker buildx --builder dev build ...
# Global to the user (not recommended so you aren't accidentally running non-dev builds against your dev instance)
$ docker buildx use dev && docker buildx build ...
```

## Connecting to the port (command line)

Full documentation is [here](https://github.com/go-delve/delve/blob/master/Documentation/usage/dlv_connect.md).

You will need a local copy of delve to connect to the debug instance.

```bash
$ dlv connect localhost:5000
```

## Connecting to the port (GoLand)

Go to `Run > Edit Configurations...`. Click on the `+` icon and select `Go Remote`. The default options for this method
are `localhost` and port `5000`. If you've changed either of these, update them in the configuration. Give the
configuration a name and save it.

After starting the above, you can set breakpoints and interact with buildkit.

## Limitations

The default version of the debug image isn't very useful if you need to debug startup issues. The default includes
the `--continue` option to delve on the command line so the program starts immediately instead of waiting for a
client to connect.

This mimics how buildkit runs as a release image and is useful enough for most debugging sessions, but may not be
useful in others. If you need to debug startup issues, you can go into the Dockerfile and remove `--continue` from
the command line options.
