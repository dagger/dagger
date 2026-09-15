# Relative to current directory
Source config.tape

Output features/secrets-env.gif

Sleep 1s

Type "dagger call github-api --token=env://TOKEN"
Sleep 1s
Enter

# Wait for the system prompt
Wait@120s

Sleep 3s
