# Relative to current directory
Source config.tape

Output quickstart/basics/terminal.gif

Source shell.tape

Type "container | from alpine | with-new-file /hi.txt "Hello from Dagger!" | terminal"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
# regex issue: https://github.com/charmbracelet/vhs/issues/592
Wait+Screen@120s /\x{2F}tmp\x{2F}hello-from-dagger/

Sleep 1s

# Wait for the system prompt
Wait

Type "uname -a"
Sleep 500ms
Enter

Type "whoami"
Sleep 500ms
Enter

Type "ls"
Sleep 500ms
Enter

Type "cat hi.txt"
Sleep 500ms
Enter

# Wait for the system prompt
Wait

Ctrl+D
