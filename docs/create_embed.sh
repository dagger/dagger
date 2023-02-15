#!/bin/bash

set -euo pipefail


TOKEN=${TOKEN:?"TOKEN env var not set"}
FILE=${1:?usage: create_embed.sh <filename.\{ts,js,mjs,py,go\}>}


content=$(cat $FILE)
ext=${1##*.}



case "$ext" in
	"go")
query='
{
  container {
    from(address: "golang") {
      withWorkdir(path: "/usr/src/app") {
        withExec(args: ["sh", "-c", "go mod init test && go get dagger.io/dagger@main"]) {
          withNewFile(
            contents: """'"$content"'"""
            path: "main.go"
          ) {
            withExec(args: ["go", "run", "main.go"], experimentalPrivilegedNesting: true) {
              stdout
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
    from(address: "python:3-slim") {
      withWorkdir(path: "/usr/src/app") {
        withExec(args: ["sh", "-c", "pip install dagger-io"]) {
          withNewFile(
            contents: """'"$content"'"""
            path: "main.py"
          ) {
            withExec(args: ["python", "main.py"], experimentalPrivilegedNesting: true) {
              stdout
            }
          }
        }
      }
    }
  }
}
'
lang='python'
	;;
	"ts") echo 2 or 3
query='
{
  container {
    from(address: "node:slim") {
      withWorkdir(path: "/usr/src/app") {
        withExec(args: ["sh", "-c", "npm install @dagger.io/dagger tsc --save-dev && npm pkg set type=module &&  npx tsc --module esnext --moduleResolution node --init"]) {
          withNewFile(
            contents: """'"$content"'"""
            path: "index.ts"
          ) {
            withExec(args: ["node", "--loader", "ts-node/esm", "index.ts"], experimentalPrivilegedNesting: true) {
              stdout
            }
          }
        }
      }
    }
  }
}
'
lang='ts'
	;;
	"js") echo 2 or 3
query='
{
  container {
    from(address: "node:slim") {
      withWorkdir(path: "/usr/src/app") {
        withExec(args: ["sh", "-c", "npm install @dagger.io/dagger --save-dev && npm pkg set type=module"]) {
          withNewFile(
            contents: """'"$content"'"""
            path: "index.js"
          ) {
            withExec(args: ["node", "index.js"], experimentalPrivilegedNesting: true) {
              stdout
            }
          }
        }
      }
    }
  }
}
'
lang='js'
	;;
	"mjs") echo 2 or 3
query='
{
  container {
    from(address: "node:slim") {
      withWorkdir(path: "/usr/src/app") {
        withExec(args: ["sh", "-c", "npm install @dagger.io/dagger --save-dev && npm pkg set type=module"]) {
          withNewFile(
            contents: """'"$content"'"""
            path: "index.mjs"
          ) {
            withExec(args: ["node", "index.mjs"], experimentalPrivilegedNesting: true) {
              stdout
            }
          }
        }
      }
    }
  }
}
'
lang='javascript'
	;;
	*) echo "Unsupported file extension: ["$ext"] " && exit 1
	;;
esac

escaped=$(echo "$query" | jq -Rsa . )


id=$(curl 'https://api.dagger.cloud/playgrounds/share' -v -sS -H 'content-type: application/json' -H "authorization: bearer ${TOKEN}" --data '{"query":'"$escaped"', "lang": "'"$lang"'"}')

echo "https://play.dagger.cloud/embed/$id"
