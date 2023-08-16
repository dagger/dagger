#!/bin/bash

set -euo pipefail

TOKEN=${TOKEN:?"TOKEN env var not set"}
FILE=${1:?usage: create_embed_qs.sh <filename.\{js,mjs,py,go,exs\}>}


content=$(cat $FILE)
ext=${1##*.}



case "$ext" in
	"go")
query='
{
  container {
    from(address: "golang") {
      withExec(
        args: ["sh", "-c", "git clone https://github.com/dagger/hello-dagger /usr/src/app/hello-dagger"]
      ) {
        withWorkdir(path: "/usr/src/app/hello-dagger") {
          withExec(
            args: ["sh", "-c", "mkdir ci && go mod init test && go get dagger.io/dagger@latest"]
          ) {
            withNewFile(
              contents: """'"$content"'"""
              path: "ci/main.go"
            ) {
              withExec(args: ["go", "run", "ci/main.go"], experimentalPrivilegedNesting: true) {
                stdout
              }
            }
          }
        }
      }
    }
  }
}'
lang='go'
	;;
	"py") echo 2 or 3
query='
{
  container {
    from(address: "python:3") {
      withExec(
        args: ["sh", "-c", "git clone https://github.com/dagger/hello-dagger /usr/src/app/hello-dagger"]
      ) {
        withWorkdir(path: "/usr/src/app/hello-dagger") {
          withExec(args: ["sh", "-c", "mkdir ci && pip install dagger-io"]) {
            withNewFile(
              contents: """'"$content"'"""
              path: "ci/main.py"
            ) {
              withExec(args: ["python", "ci/main.py"], experimentalPrivilegedNesting: true) {
                stdout
              }
            }
          }
        }
      }
    }
  }
}'
lang='python'
	;;
	"js") echo 2 or 3
query='
{
  container {
    from(address: "node") {
      withExec(
        args: ["sh", "-c", "git clone --depth=1 https://github.com/dagger/hello-dagger /usr/src/app/hello-dagger"]
      ) {
        withWorkdir(path: "/usr/src/app/hello-dagger") {
          withExec(args: ["sh", "-c", "mkdir ci && npm install @dagger.io/dagger --save-dev && npm pkg set type=module"]) {
            withNewFile(
              contents: """'"$content"'"""
              path: "ci/main.js"
            ) {
              withExec(args: ["node", "ci/main.js"], experimentalPrivilegedNesting: true) {
                stdout
              }
            }
          }
        }
      }
    }
  }
}'
lang='javascript'
	;;
	"mjs") echo 2 or 3
query='
{
  container {
    from(address: "node") {
      withExec(
        args: ["sh", "-c", "git clone --depth=1 https://github.com/dagger/hello-dagger /usr/src/app/hello-dagger"]
      ) {
        withWorkdir(path: "/usr/src/app/hello-dagger") {
          withExec(args: ["sh", "-c", "mkdir ci && npm install @dagger.io/dagger --save-dev && npm pkg set type=module"]) {
            withNewFile(
              contents: """'"$content"'"""
              path: "ci/main.mjs"
            ) {
              withExec(args: ["node", "ci/main.mjs"], experimentalPrivilegedNesting: true) {
                stdout
              }
            }
          }
        }
      }
    }
  }
}'
lang='javascript'
	;;
	"exs") echo 2 or 3
query='
{
  container {
    from(address: "hexpm/elixir:1.15.4-erlang-25.3.2.5-alpine-3.18.2") {
      withExec(args: ["sh", "-c", "apk add git"]) {
        withExec(
          args: ["sh", "-c", "git clone --depth=1 https://github.com/dagger/hello-dagger /usr/src/app/hello-dagger"]
        ) {
          withWorkdir(path: "/usr/src/app/hello-dagger") {
            withExec(args: ["sh", "-c", "mkdir ci"]) {
              withNewFile(
                contents: """'"$content"'""",
                path: "ci/main.exs"
              ) {
                withExec(args: ["elixir", "ci/main.exs"], experimentalPrivilegedNesting: true) {
                  stdout
                }
              }
            }
          }
        }
      }
    }
  }
}'
lang='elixir'
	;;
	*) echo "Unsupported file extension: ["$ext"] " && exit 1
	;;
esac

escaped=$(echo "$query" | jq -Rsa . )

id=$(curl 'https://api.dagger.cloud/playgrounds/share' -v -sS -H 'content-type: application/json' -H "authorization: bearer ${TOKEN}" --data '{"query":'"$escaped"', "lang": "'"$lang"'"}')

echo "https://play.dagger.cloud/embed/$id"
