# Relative to current directory
Source config.tape

Output quickstart/publish.gif

Sleep 1s

Type "dagger -c 'publish .'"
Sleep 1s
Enter

# Wait for the system prompt
Wait@120s

Sleep 3s
