# Relative to current directory
Source config.tape

Output features/shell-help.gif

Sleep 1s

Type "dagger -m github.com/sagikazarmark/daggerverse/helm"
Sleep 1s
Enter

Hide

# Wait for the shell to load
Wait+Screen@120s /Dagger interactive shell/
Ctrl+L

Show

Sleep 1.5s

Type ". | .help"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
Wait+Screen@120s /Helm/

Sleep 3s
Ctrl+L
Sleep 1s

Type ".help create"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
Wait+Screen@120s /Create a new chart directory/

Sleep 3s
Ctrl+L
Sleep 1s

Type "create my-chart | .help"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
Wait+Screen@120s /HelmChart/

Sleep 3s
Ctrl+L
Sleep 1s

Type "create my-chart | package | .help"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
Wait+Screen@120s /HelmPackage/

Sleep 2s

Ctrl+D

# Wait for the system prompt
Wait
