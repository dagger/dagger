# Relative to current directory
Source config.tape

Output features/secrets-file.gif

Sleep 1s

Type "dagger call github-api --token=file://token"
Sleep 1s
Enter

# Wait for the system prompt
Wait@120s

Sleep 3s
