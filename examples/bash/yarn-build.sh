#!/bin/bash

set -eu


# Load app source code from working directory
source=$({ cloak do <<EOF
{
    host {
        workdir {
            read {
                id
            }
        }
    }
}
EOF
} | jq -r .host.workdir.read.id)


# Install yarn in a container
image=$({ cloak do <<EOF
{
    core {
        image(ref: "index.docker.io/alpine") {
            exec(input: { args: ["apk", "add", "yarn", "git", "openssh"] }) {
                fs {
                    id
                }
            }
        }
    }
}
EOF
} | jq -r .core.image.exec.fs.id)


# Run `yarn install` in a container
sourceAfterInstall=$({ cloak do --set image="$image" --set source="$source" <<EOF
query (\$image: FSID!, \$source: FSID!) {
    core {
        filesystem(id: \$image) {
            exec(
                input: {
                    args: ["yarn", "install"]
                    mounts: [{ fs: \$source, path: "/src" }]
                    workdir: "/src"
                    env: [
                        { name: "YARN_CACHE_FOLDER", value: "/cache" }
                        {
                            name: "GIT_SSH_COMMAND"
                            value: "ssh -o StrictHostKeyChecking=no"
                        }
                    ]
                    cacheMounts: {
                        name: "yarn"
                        path: "/cache"
                        sharingMode: "locked"
                    }
                    sshAuthSock: "/ssh-agent"
                }
            ) {
                # Retrieve modified source
                mount(path: "/src") {
                    id
                }
            }
        }
    }
}
EOF
} | jq -r .core.filesystem.exec.mount.id)


sourceAfterBuild=$({ cloak do --set image="$image" --set sourceAfterInstall="$sourceAfterInstall" <<EOF
query (\$image: FSID!, \$sourceAfterInstall: FSID!) {
    core {
        filesystem(id: \$image) {
            exec(
                input: {
                    args: ["yarn", "run", "react-scripts", "build"]
                    mounts: [{ fs: \$sourceAfterInstall, path: "/src" }]
                    workdir: "/src"
                    env: [
                        { name: "YARN_CACHE_FOLDER", value: "/cache" }
                        {
                            name: "GIT_SSH_COMMAND"
                            value: "ssh -o StrictHostKeyChecking=no"
                        }
                    ]
                    cacheMounts: {
                        name: "yarn"
                        path: "/cache"
                        sharingMode: "locked"
                    }
                    sshAuthSock: "/ssh-agent"
                }
            ) {
                # Retrieve modified source
                mount(path: "/src") {
                    id
                }
            }
        }
    }
}
EOF
} | jq -r .core.filesystem.exec.mount.id)
