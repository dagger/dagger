Source ../common/config.tape

Output ../../recordings/modules.gif

Source ../common/start.tape

Type `dagger install github.com/dagger/dagger/modules/wolfi`
Enter
Sleep 15s

Type `dagger install github.com/jpadams/daggerverse/trivy`
Enter
Sleep 20s

Type `dagger`
Enter
Sleep 15s
Ctrl+L

Type `ctr=$(wolfi | container)`
Enter
Sleep 6s

Type `trivy | scan-container $ctr`
Enter
Sleep 15s
