
# Commands


```
Global flags

  -w, --workspace     Select a workspace (default: $HOME/.dagger)
  -e, --env           Select an environment (default: computed from current directory. See ENVIRONMENT SELECTION)
  

dagger info			Show contextual information

    -q EXPR       Filter output with a cue expression


dagger catalog               Manage the dagger package catalog

    --select-stdlib VERSION   Select a version of the standard library (default: stable)
    --add-dir PATH            Add a local directory to the package catalog
    --rm-dir PATH             Remove a local directory from the package catalog
    --add-git REMOTE#REF      Add a git repository to the package catalog
    --rm-git REMOTE#REF       Remove a git repository from the package catalog
    --rm-all                  Remove all local directories and git repositories from the package catalog


dagger init      Initialize an environment

    --interactive no|yes|auto         Specify whether to present user with interactive setup  
    -n, --name NAME                   Specify the environment's name. (default: the name of the current directory)
    
    -b, --base ENV_ID | ENV_NAME | PACKAGE       Load base configuration from the given cue package or environment
                                      Examples:
                                         cue package: `dagger new --base dagger.io/templates/jamstack`
                                         env name: `dagger new --base acme-prod`
                                         env ID: `dagger new --base acme-prod-happy-panda-8411`

    --input-string KEY=STRING
    --input-dir KEY=PATH
    --input-secret KEY[=PATH]
    --input-json KEY=JSON
    --input-git KEY=REMOTE#REF
    --input-container KEY=REF


dagger destroy                  Destroy an environment
    
    -f, --force               Destroy environment state even if cleanup pipelines fail to complete (EXPERTS ONLY)


dagger change                    Make a change to the current environment

    --interactive no|yes|auto         Specify whether to present user with interactive setup  
    -n, --name NAME                   Specify the environment's name. (default: name of current directory)
    
    -b, --base ENV_ID | ENV_NAME | PACKAGE       Load base configuration from the given cue package or environment
                                      Examples:
                                         cue package: `dagger new --base dagger.io/templates/jamstack`
                                         env name: `dagger new --base acme-prod`
                                         env ID: `dagger new --base acme-prod-happy-panda-8411`

    --input-string KEY=STRING
    --input-dir KEY=PATH
    --input-secret KEY[=PATH]
    --input-json KEY=JSON
    --input-git KEY=REMOTE#REF
    --rollback VERSION              Roll back environment state to the specified version.
                                      Changes are re-computed and dagger cannot guarantee that all external state will be perfectly reverted.
                                      (aka "roll forward").


dagger query [EXPR...]         Query an environment's state

    EXPR may be any valid CUE expression. The expression is evaluated against the environment state,
    and written to standard output.

    -v,--version                      Query a specific version of the environment (default: last known version)
    -f,--format cue|json|yaml|text    Specify output format (default: cue)
    -i,--import PACKAGE               Specify cue packages to import when evaluating the query


dagger history  Show an environment's history of changes

    -q EXPR          Filter output with a cue expression


dagger download [KEY...]               Download data from an environment

    -o PATH         Select a destination directory (default: .)


dagger sync       Synchronize local state to Dagger Cloud (optional)
dagger login			Login to Dagger Cloud (optional)
dagger logout			Logout from Dagger Cloud (optional)
```

# Environment selection

Almost all dagger commands take place within an environment.

Before executing each command, `dagger` selects which environment to execute the command in. The selection process is the following:

1. If an environment is explicitly specified with `--env` or `-e`, use that.
2. If .daggerenv exists in the current directory, use its contents.
