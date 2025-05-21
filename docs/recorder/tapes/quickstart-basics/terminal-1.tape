Source ../common/config.tape

Output ../../recordings/terminal-1.webm

Source ../common/shell-nomod.tape

Source ../common/start.tape

Type `container | from alpine | terminal`
Sleep 1s
Enter
Sleep 2s

Type "uname -a"
Sleep 1s
Enter
Sleep 2s

Type "whoami"
Sleep 1s
Enter
Sleep 2s

Type "ls"
Sleep 1s
Enter
Sleep 2s

Type "ls /etc"
Sleep 1s
Enter
Sleep 2s

Type "cat /etc/alpine-release"
Sleep 1s
Enter
Sleep 5s
