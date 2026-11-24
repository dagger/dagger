#!/bin/sh
# gen.sh DIR VARIANT FUNCS: write a Dang module "variant" into DIR whose
# source differs from the other variants only by a comment, with FUNCS
# functions written against the Dagger API (so its compilation infers many
# API types) besides `ping`, the one the workload calls.
set -eu
dir=$1
variant=$2
funcs=$3
mkdir -p "$dir"
cat >"$dir/dagger.json" <<'EOF'
{"name": "variant", "engineVersion": "latest", "sdk": {"source": "dang"}}
EOF
{
  echo "# variant $variant"
  echo "type Variant {"
  echo "  ping: String! { \"pong\" }"
  i=0
  while [ "$i" -lt "$funcs" ]; do
    cat <<EOF

  """
  Function $i: a container built from the Dagger API.
  """
  f$i(tag: String! = "3.20", msg: String! = "hello $i"): Container! {
    let src = directory.withNewFile("f$i.txt", msg).withNewDirectory("sub$i")
    let ctr = container.from("alpine:" + tag)
      .withEnvVariable("K$i", msg)
      .withDirectory("/src", src)
      .withWorkdir("/src")
      .withExec(["sh", "-c", "echo " + msg + " > out$i"])
    if (msg == "") {
      ctr.withFile("/out", ctr.file("/src/out$i"))
    } else {
      ctr.withMountedCache("/cache", cacheVolume("c$i")).withUser("root")
    }
  }
EOF
    i=$((i + 1))
  done
  echo "}"
} >"$dir/main.dang"
