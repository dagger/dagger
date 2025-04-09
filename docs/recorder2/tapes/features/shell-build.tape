# Relative to current directory
Source config.tape

Output features/shell-build.gif

Source shell.tape

Type "container | from cgr.dev/chainguard/wolfi-base | with-exec apk add go | with-directory /src https://github.com/golang/example#master | with-workdir /src/hello | with-exec -- go build -o hello . | file ./hello | export /tmp/hello-from-dagger"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
# regex issue: https://github.com/charmbracelet/vhs/issues/592
Wait+Screen@120s /\x{2F}tmp\x{2F}hello-from-dagger/

Sleep 1s

Ctrl+D

# Wait for the system prompt
Wait

Type "/tmp/hello-from-dagger"
Sleep 500ms
Enter

# Wait for the system prompt
Wait
