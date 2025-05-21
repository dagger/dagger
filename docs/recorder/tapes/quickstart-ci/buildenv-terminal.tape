# Relative to current directory
Source config.tape

Output quickstart/ci/buildenv-terminal.gif

Source shell.tape

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

Type "exit"
Sleep 1s
Enter

Sleep 5s
