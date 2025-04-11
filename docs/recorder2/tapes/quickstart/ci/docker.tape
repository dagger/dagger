# Relative to current directory
Source config.tape

Output quickstart/ci/docker.gif

# Update this image id based on the output of publish.tape
Type "docker run --rm --detach --publish 8080:80 ttl.sh/hello-dagger-833337"
Sleep 1s
Enter

Wait+Screen@120s /<\x{2F}html>/

Sleep 5s
