Source ../common/config.tape

Output ../../recordings/publish.gif

Hide
Type `cd hello-dagger`
Enter
Show

Source ../common/shell-mod.tape

Source ../common/start.tape

Type `publish`
Sleep 1s
Enter

Wait+Screen@120s /@sha256/
Sleep 5s
