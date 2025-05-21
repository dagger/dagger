Source ../common/config.tape

Output ../../recordings/build-service.gif

Hide
Type `cd hello-dagger`
Enter
Show

Source ../common/shell-mod.tape

Source ../common/start.tape

Type `build | as-service | up --ports=8080:80`
Enter

Sleep 120s
