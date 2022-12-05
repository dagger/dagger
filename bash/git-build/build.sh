#!/bin/bash

# get Go examples source code repository
source=$(dagger --debug query <<EOF | jq -r .git.branch.tree.id
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
build=$(dagger --debug query <<EOF | jq -r .container.from.withMountedDirectory.withWorkdir.withExec.file.export
{
  container {
    from(address:"golang:latest") {
      withMountedDirectory(path:"/src", source:"$source") {
        withWorkdir(path:"/src") {
          withExec(args:["go", "build", "-o", "dagger-builds-hello", "./hello/hello.go"]) {
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

# check build result and display message
if [ "$build" == "true" ]
then
    echo "Build successful"
else
    echo "Build unsuccessful"
fi