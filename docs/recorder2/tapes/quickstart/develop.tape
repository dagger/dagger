# Relative to current directory
Set Theme "Catppuccin Mocha"
Output develop.webm

Sleep 1s

Type "dagger call develop --assignment 'make the main page green'"
Sleep 1s
Enter

# Wait for the system prompt
Wait@240s

Sleep 3s
