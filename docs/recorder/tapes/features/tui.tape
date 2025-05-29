# Relative to current directory
Source config.tape

Output features/tui.gif

Sleep 1s

Type "dagger -m github.com/jpadams/daggerverse/trivy@v0.5.0 call scan-container --ctr=index.docker.io/alpine:latest"
Sleep 1s
Enter

# Wait for the system prompt
Wait@120s

Sleep 3s
