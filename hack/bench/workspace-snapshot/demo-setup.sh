#!/usr/bin/env bash
set -euo pipefail

demo_root=${WORKSPACE_SNAPSHOT_DEMO_ROOT:-/tmp/workspace-snapshot-demo}
source_root=$(git rev-parse --show-toplevel)
demo_repo=$demo_root/dagger

case "$demo_root" in
  /tmp/workspace-snapshot-demo*) ;;
  *)
    echo "refusing demo root outside /tmp/workspace-snapshot-demo*: $demo_root" >&2
    exit 1
    ;;
esac

if [[ -e "$demo_root" ]]; then
  if [[ ${1:-} != "--reset" ]]; then
    echo "$demo_root already exists; pass --reset to recreate it" >&2
    exit 1
  fi
  rm -rf -- "$demo_root"
fi
mkdir -p "$demo_root"

base_sha=$(git -C "$source_root" rev-parse 'origin/main^{commit}')
git clone --quiet --local --no-hardlinks --no-checkout "$source_root" "$demo_repo"
git -C "$demo_repo" remote set-url origin https://github.com/dagger/dagger.git
git -C "$demo_repo" update-ref refs/remotes/origin/main "$base_sha"
git -C "$demo_repo" checkout --quiet -B workspace-demo "$base_sha"
git -C "$demo_repo" branch --set-upstream-to origin/main workspace-demo >/dev/null

modified=0
while IFS= read -r -d '' relative_path; do
  absolute_path=$demo_repo/$relative_path
  if [[ -f "$absolute_path" && ! -L "$absolute_path" ]]; then
    dd if=/dev/urandom of="$absolute_path" bs=256K count=1 status=none
    modified=$((modified + 1))
    [[ $modified -eq 64 ]] && break
  fi
done < <(git -C "$demo_repo" ls-files -z)
if [[ $modified -ne 64 ]]; then
  echo "fixture has fewer than 64 regular tracked files" >&2
  exit 1
fi

for index in $(seq 0 127); do
  untracked_dir=$demo_repo/benchmark-untracked/d$((index % 16))
  mkdir -p "$untracked_dir"
  dd if=/dev/urandom of="$untracked_dir/f$index" bs=128K count=1 status=none
done
printf 'workspace-snapshot-demo\n' >"$demo_repo/benchmark-probe.txt"

cat >"$demo_root/query.graphql" <<'EOF'
{
  currentWorkspace {
    directory(path: "/") {
      digest
      untracked: file(path: "benchmark-probe.txt") { contents }
    }
  }
}
EOF

printf '%s\n' "$base_sha" >"$demo_root/base-sha"

echo "fixture:  $demo_repo"
echo "base:     $base_sha"
echo "worktree: $(du -sh --exclude=.git "$demo_repo" | cut -f1)"
echo ".git:     $(du -sh "$demo_repo/.git" | cut -f1)"
echo "delta:    64 tracked x 256 KiB + 128 untracked x 128 KiB"
echo
git -C "$demo_repo" status --short | tail -12
