# Relative to current directory
Source config.tape

Output features/shell-curl.gif

Source shell.tape

Type "container | from alpine | with-exec apk add curl | with-exec -- curl -L https://dagger.io | stdout"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
# regex issue: https://github.com/charmbracelet/vhs/issues/592
Wait+Screen@120s /<\x{2F}html>/

Sleep 1s

Ctrl+D

# Wait for the system prompt
Wait
