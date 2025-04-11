# Relative to current directory
Source config.tape

Output quickstart/ci/build-service.gif

Source shell.tape

Type "build | as-service | up --ports=8080:80"
Sleep 1s
Enter

# Wait for the system prompt
Wait+Screen@120s /tunnel started/

Sleep 5s
