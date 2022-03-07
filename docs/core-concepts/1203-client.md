---
slug: /1203/client
displayed_sidebar: europa
---

# Interacting with the client

`dagger.#Plan` has a `client` field that allows interaction with the local machine where the `dagger` command line client is run. You can:

- Read and write files and directories;
- Use local sockets;
- Load environment variables;
- Run commands;
- Get current platform.

## Accessing the file system

You may need to load a local directory as a `dagger.#FS` type in your plan:

```cue
dagger.#Plan & {
    // Path may be absolute, or relative to current working directory
    client: filesystem: ".": read: {
        // CUE type defines expected content
        contents: dagger.#FS
        exclude: ["node_modules"]
    }
    actions: {
        ...
        copy: docker.Copy & {
            contents: client.filesystem.".".read.contents
        }
        ...
    }
}
```

It’s also easy to write a file locally:

```cue
dagger.#Plan & {
    client: filesystem: "config.yaml": write: {
        contents: yaml.Marshal(actions.pull.output.config)
    }
    actions: {
        pull: docker.#Pull & {
            source: "alpine"
        }
    }
}
```

## Using a local socket

You can use a local socket in an action:

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

<Tabs defaultValue="unix" groupId="client-env">

<TabItem value="unix" label="Linux/macOS">

```cue
dagger.#Plan & {
    client: filesystem: "/var/run/docker.sock": read: {
        contents: dagger.#Service
    }

    actions: {
        image: alpine.#Build & {
            packages: "docker-cli": {}
        }
        run: docker.#Run & {
            input: image.output
            mounts: docker: {
                dest:     "/var/run/docker.sock"
                contents: client.filesystem."/var/run/docker.sock".read.contents
            }
            command: {
                name: "docker"
                args: ["info"]
            }
        }
    }
}
```

</TabItem>

<TabItem value="windows" label="Windows">

```cue
dagger.#Plan & {
    client: filesystem: "//./pipe/docker_engine": read: {
        contents: dagger.#Service
        type: "npipe"
    }

    actions: {
        image: alpine.#Build & {
            packages: "docker-cli": {}
        }
        run: docker.#Run & {
            input: image.output
            mounts: docker: {
                dest:     "/var/run/docker.sock"
                contents: client.filesystem."//./pipe/docker_engine".read.contents
            }
            command: {
                name: "docker"
                args: ["info"]
            }
        }
    }
}
```

</TabItem>
</Tabs>

## Environment variables

Environment variables can be read from the local machine as strings or secrets, just specify the type:

```cue
dagger.#Plan & {
    client: env: {
        GITLAB_USER: string
        GITLAB_TOKEN: dagger.#Secret
    }
    actions: {
        pull: docker.#Pull & {
            source: "registry.gitlab.com/myuser/myrepo"
            auth: {
                username: client.env.GITLAB_USR
                secret: client.env.GITLAB_TOKEN
            }
        }
    }
}
```

## Running commands

Sometimes you need something more advanced that only a local command can give you:

```cue
dagger.#Plan & {
    client: commands: {
        os: {
            name: "uname"
            args: ["-s"]
        }
        arch: {
            name: "uname"
            args: ["-m"]
        }
    }
    actions: {
        build: docker.#Run & {
            env: {
                CLIENT_OS: client.commands.os.stdout
                CLIENT_ARCH: client.commands.arch.stdout
            }
        }
    }
}
```

You can also capture `stderr` for errors and provide `stdin` for input.

## Platform

If you need the current platform though, there’s a more portable way than running `uname` like in the previous example:

```cue
dagger.#Plan & {
    client: platform: _

    actions: {
        build: docker.#Run & {
            env: {
                CLIENT_OS: client.platform.os
                CLIENT_ARCH: client.platform.arch
            }
        }
    }
}
```
