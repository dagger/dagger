Source ../common/config.tape

Output ../../recordings/modules.webm

Source ../common/start.tape

Type `dagger install github.com/dagger/dagger/modules/wolfi`
Sleep 1s
Enter
Sleep 5s

Type `dagger install github.com/jpadams/daggerverse/trivy`
Sleep 1s
Enter
Sleep 5s

Type `dagger`
Sleep 1s
Enter
Sleep 5s

Ctrl+L
Type `ctr=$(wolfi | container)`
Sleep 1s
Enter
Sleep 4s

Type `trivy | scan-container $ctr`
Sleep 1s
Enter
Sleep 15s
