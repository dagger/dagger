Source ../common/config.tape

Output ../../recordings/terminal-2.webm

Source ../common/shell-nomod.tape

Source ../common/start.tape

Type `container | from alpine | with-new-file /hi.txt "Hello from Dagger!" | terminal`
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

Type "cat hi.txt"
Sleep 1s
Enter
Sleep 2s
