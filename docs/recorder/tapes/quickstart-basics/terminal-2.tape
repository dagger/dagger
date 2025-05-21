Source ../common/config.tape

Output ../../recordings/terminal-2.gif

Source ../common/shell-nomod.tape

Source ../common/start.tape

Type `container | from alpine | with-new-file /hi.txt "Hello from Dagger!" | terminal`
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

Type "cat hi.txt"
Enter
Sleep 2s
