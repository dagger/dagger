# Relative to current directory
Source config.tape

Output quickstart/basics/publish-shell.gif

Source shell-nomod.tape

Type `container | from alpine | with-new-file /hi.txt "Hello from Dagger!" | with-entrypoint cat /hi.txt | publish ttl.sh/hello`
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
# regex issue: https://github.com/charmbracelet/vhs/issues/592
Wait+Screen@120s /sha256/

Sleep 5s

Ctrl+D

# Wait for the system prompt
Wait
