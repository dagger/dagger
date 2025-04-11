# Relative to current directory
Source config.tape

Output quickstart/ci/publish.gif

Source shell.tape

Type "publish"
Sleep 1s
Enter

Wait+Screen@120s /@sha256/

Sleep 5s
