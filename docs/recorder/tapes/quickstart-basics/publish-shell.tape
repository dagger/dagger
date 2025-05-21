Source ../common/config.tape

Output ../../recordings/publish-shell.gif

Source ../common/shell-nomod.tape

Source ../common/start.tape

Type `container | from alpine | with-new-file /hi.txt "Hello from Dagger!" | with-entrypoint cat /hi.txt | publish ttl.sh/hello`
Enter
Sleep 5s
