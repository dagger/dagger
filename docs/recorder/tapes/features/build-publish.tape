# Relative to current directory
Source config.tape

Output features/build-publish.gif

Sleep 1s

Type "dagger call build --src=https://github.com/golang/example#master:/hello --arch=amd64 --os=linux publish --address=ttl.sh/my-img"
Sleep 1s
Enter

# Wait for the system prompt
Wait@120s

Sleep 3s
