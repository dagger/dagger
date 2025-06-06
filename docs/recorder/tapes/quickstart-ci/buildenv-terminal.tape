Source ../common/config.tape

Output ../../recordings/buildenv-terminal.webm

Hide
Type `cd hello-dagger`
Enter
Show

Source ../common/shell-mod.tape

Source ../common/start.tape

Type "build-env | terminal --cmd=bash"
Sleep 1s
Enter

Wait+Screen@120s /root@/

Type "uname -a"
Sleep 1s
Enter

Wait+Screen@120s /root@/

Type "ls"
Sleep 1s
Enter

Wait+Screen@120s /root@/

Type "cat package.json"
Sleep 1s
Enter

Wait+Screen@120s /root@/

Type "npm list"
Sleep 1s
Enter

Wait+Screen@120s /root@/
Sleep 5s
