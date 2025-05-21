Source ../common/config.tape

Output ../../recordings/modules.gif

Source ../common/start.tape

Type `dagger install github.com/dagger/dagger/modules/wolfi`
Sleep 1s
Enter
Sleep 15s

Type `dagger install github.com/jpadams/daggerverse/trivy`
Sleep 1s
Enter
Sleep 20s

Type `dagger`
Sleep 1s
Enter
Sleep 15s
Ctrl+L

Type `ctr=$(wolfi | container)`
Sleep 1s
Enter
Sleep 6s

Type `trivy | scan-container $ctr`
Sleep 1s
Enter
Sleep 15s
