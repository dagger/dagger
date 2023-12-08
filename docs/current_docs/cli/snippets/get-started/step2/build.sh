#!/bin/bash

alpine=$(dagger query <<EOF | jq -r .container.from.withExec.stdout
{
  container {
    from(address:"alpine:latest") {
      withExec(args:["uname", "-nrio"]) {
        stdout
      }
    }
  }
}
EOF
)

echo $alpine
