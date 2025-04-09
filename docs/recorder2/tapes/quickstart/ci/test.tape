# Relative to current directory
Source config.tape

Output quickstart/ci/test.gif

Source shell.tape

Type "test"
Sleep 1s
Enter

Wait+Screen@120s /\x{2F}tmp\x{2F}hello-from-dagger/

Sleep 1s

Ctrl+D
