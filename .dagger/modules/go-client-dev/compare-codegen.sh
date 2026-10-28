#!/bin/sh
set -u

failed=0

compare() {
	if cmp -s "$2" "$3"; then
		echo "same       $1"
	else
		echo "DIFFERENT  $1"
		diff -u "$2" "$3" | head -n 200
		failed=1
	fi
}

compare_tree() {
	if changes=$(diff -rq "$2" "$3"); then
		echo "same       $1"
	else
		echo "DIFFERENT  $1"
		echo "$changes"
		failed=1
	fi
}

check() {
	if [ "$2" = "$3" ]; then
		echo "same       $1"
	else
		echo "DIFFERENT  $1: $2, not $3"
		failed=1
	fi
}

echo "candidate ref       $CANDIDATE_REF"
echo "candidate commit    $CANDIDATE_COMMIT"
echo "engine commit       $(cat /capture/engine-commit) ($ENGINE_SOURCE)"
echo "engine binary       sha256:$(cat /capture/engine-binary.sha256)"
echo "engine rootfs       $(cat /capture/engine-rootfs.digest)"
echo "schema sha256       $(cat /capture/schema.sha256)"
echo "schema version      $(grep -o '"__schemaVersion": *"[^"]*"' /capture/schema.json | cut -d'"' -f4)"
echo "generator repo      $GENERATOR_REPO"
echo "generator commit    $GENERATOR_COMMIT"
if [ "$GENERATOR_COMMIT" != "$PINNED_GENERATOR_COMMIT" ]; then
	echo "note       generator commit is not the pinned $PINNED_GENERATOR_COMMIT"
fi
echo

check "schema sha256" "$(sha256sum /capture/schema.json | cut -d' ' -f1)" "$(cat /capture/schema.sha256)"
for f in core/core.gen.go dag/dag.gen.go; do
	compare "embedded and external $f" "/embedded/$f" "/external/$f"
done
for f in core/core.gen.go dag/dag.gen.go core/codecs.gen.go; do
	compare "candidate and external $f" "/candidate/$f" "/external/$f"
	compare "external and its rerun $f" "/external/$f" "/external-rerun/$f"
done
compare_tree "candidate and embedded sdk/go" /candidate /embedded
compare_tree "candidate and external sdk/go" /candidate /external

exit $failed
