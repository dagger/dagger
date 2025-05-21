Source ../common/config.tape

Output ../../recordings/terminal-1.gif

Source ../common/shell-nomod.tape

Source ../common/start.tape

Type `container | from alpine | terminal`
Enter
Sleep 2s

Type "uname -a"
Enter
Sleep 2s

Type "whoami"
Enter
Sleep 2s

Type "ls"
Enter
Sleep 2s

Type "ls /etc"
Enter
Sleep 2s

Type "cat /etc/alpine-release"
Enter
Sleep 5s
