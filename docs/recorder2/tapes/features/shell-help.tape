# Relative to current directory
Source config.tape

Output features/shell-help.gif

Type "dagger -m github.com/sagikazarmark/daggerverse/helm"
Sleep 1s
Enter

# Wait for the shell to load
Wait+Screen@120s /Dagger interactive shell/

Sleep 1s

Type ". | .help"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
Wait+Screen@120s /Helm/
# Wait+Screen@120s /for more information on a function/

Sleep 1s

Type ".help create"
Sleep 1s
Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
Wait+Screen@120s /Create a new chart/
# Wait+Screen@120s /for available functions/

Sleep 1s

######
# Not sure why, but it dies here
######

#Type "create my-chart | .help"
#Sleep 1s
#Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
#Wait+Screen@120s /HelmChart/
# Wait+Screen@120s /for more information on a function/

#Sleep 1s

#Type "create my-chart | package | .help"
#Sleep 1s
#Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
#Wait+Screen@120s /HelmPackage/
# Wait+Screen@120s /for more information on a function/

#Sleep 1s

#Type "create my-chart | package | publish ttl.sh/my-chart | .help"
#Sleep 1s
#Enter

# Unfortunately Dagger Shell immediately returns the prompt, so waiting for that to appear is not an option
#Wait+Screen@120s /void/

Sleep 2s

Ctrl+D

# Wait for the system prompt
Wait
