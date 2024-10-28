#!/bin/bash --noprofile --norc -e -o pipefail

if [[ -n "$DEBUG" && "$DEBUG" != "0" ]]; then
    set -x
    env
    which dagger
    pwd
    ls -l
    ps aux
fi

# Detect if a dev engine is available, if so: use that
# We don't rely on PATH because the GHA runner messes with that
if [[ -n "$_EXPERIMENTAL_DAGGER_CLI_BIN" ]]; then
    export PATH=$(dirname "$_EXPERIMENTAL_DAGGER_CLI_BIN"):$PATH
fi

GITHUB_OUTPUT="${GITHUB_OUTPUT:=github-output.txt}"
GITHUB_STEP_SUMMARY="${GITHUB_STEP_SUMMARY:=github-summary.md}"
export NO_COLOR="${NO_COLOR:=1}" # Disable colors in dagger logs

# Ensure the command is provided as an environment variable
if [ -z "$COMMAND" ]; then
  echo "Error: Please set the COMMAND environment variable."
  exit 1
fi

tmp=$(mktemp -d)
(
    cd $tmp

    # Create named pipes (FIFOs) for stdout and stderr
    mkfifo stdout.fifo stderr.fifo

    # Set up tee to capture and display stdout and stderr
    tee stdout.txt < stdout.fifo &
    tee stderr.txt < stderr.fifo >&2 &
)

# Run the command, capturing stdout and stderr in the FIFOs
set +e
eval "$COMMAND" > $tmp/stdout.fifo 2> $tmp/stderr.fifo
EXIT_CODE=$?
set -e
# Wait for all background jobs to finish
wait

# Extra trace URL
TRACE_URL=$(sed -En 's/^Full trace at (.*)/\1/p' < $tmp/stderr.txt)

# Expose the outputs as GitHub Actions step outputs directly from the files
# Multi-line outputs are handled with the '<<EOF' syntax
{
    echo 'stdout<<EOF'
    cat "$tmp/stdout.txt"
    echo 'EOF'
    echo 'stderr<<EOF'
    cat "$tmp/stderr.txt"
    echo 'EOF'
} > "${GITHUB_OUTPUT}"

{
cat <<'.'
## Dagger trace

.

if [[ "$TRACE_URL" == *"rotate dagger.cloud token for full url"* ]]; then
    cat <<.
Cloud token must be rotated. Please follow these steps:

1. Go to [Dagger Cloud](https://dagger.cloud)
2. Click on your profile icon in the bottom left corner
3. Click on "Organization Settings"
4. Click on "Regenerate token"
5. Update the [\`DAGGER_CLOUD_TOKEN\` secret in your GitHub repository settings](https://github.com/${GITHUB_REPOSITORY:?Error: GITHUB_REPOSITORY is not set}/settings/secrets/actions/DAGGER_CLOUD_TOKEN)
.
elif [ -n "$TRACE_URL" ]; then
    echo "[$TRACE_URL]($TRACE_URL)"
else
    echo "No trace available. To setup: [https://dagger.cloud/traces/setup](https://dagger.cloud/traces/setup)"
fi

cat <<'.'

## Dagger version

```
.

dagger version

cat <<'.'
```

## Pipeline command

```bash
.

echo "DAGGER_MODULE=$DAGGER_MODULE \\"
echo " $COMMAND"

cat <<'.'
```

## Pipeline output

```
.

cat $tmp/stdout.txt

cat <<'.'
```

## Pipeline logs

```
.

tail -n 1000 $tmp/stderr.txt

cat <<'.'
```
.

} >"${GITHUB_STEP_SUMMARY}"

exit $EXIT_CODE
