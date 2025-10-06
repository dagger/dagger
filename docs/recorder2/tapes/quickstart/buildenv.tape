# Relative to current directory
Source config.tape

Output quickstart/buildenv.gif

Sleep 1s

Type "dagger -c 'build-env .'"
Sleep 1s
Enter

# Wait for the system prompt
Wait@120s

Sleep 3s
