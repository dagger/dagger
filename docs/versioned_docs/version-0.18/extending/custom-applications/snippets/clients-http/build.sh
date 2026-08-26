#!/bin/bash

# get Go examples source code repository
source=$(dagger query <<EOF | jq -r .git.branch.tree.id
{
  git(url:"https://go.googlesource.com/example") {
    branch(name:"master") {
      tree {
        id
      }
    }
  }
}
EOF
)

# mount source code repository in golang container
# build Go binary
# export binary from container to host filesystem
build=$(dagger query <<EOF | jq -r .container.from.withDirectory.withWorkdir.withExec.file.export
{
  container {
    from(address:"golang:latest") {
      withDirectory(path:"/src", directory:"$source") {
        withWorkdir(path:"/src/hello") {
          withExec(args:["go", "build", "-o", "dagger-builds-hello", "."]) {
            file(path:"./dagger-builds-hello") {
              export(path:"./dagger-builds-hello")
            }
          }
        }
      }
    }
  }
}
EOF
)

if [ -n "$build" ]; then
	echo "Build successful"
else
	echo "Build unsuccessful"
fi
