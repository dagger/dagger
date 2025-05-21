Source ../common/config.tape

Output ../../recordings/docker.gif

Source ../common/start.tape

Type `docker run --rm --detach --publish 8080:80 ttl.sh/hello-dagger-57383`
Sleep 1s
Enter

Sleep 15s
