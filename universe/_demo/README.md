Demo Flow:
1. Server team
   1. Show server cmd code + tests
   2. Run `dagger checks`
     1. oops, one failed
     2. fix the code
     3. re-run `dagger checks`, it passes now
   3. Run `dagger artifact list`
     1. run `dagger artifact export --output /tmp/server binary`, show server bin
     2. (if time to implement) run `dagger artifacts publish server-image`
   4. Show env code
     1. Base image is from "universe", explain how those are built into the codegen using "WithFunction"
     2. Checks are also implemented by just calling out to universe
     3. Artifacts, same
2. Client team
   1. Show client cmd code + tests
   2. Run `dagger checks`
   3. Run `dagger do publish`, does a fake publish to pypi
     1. example where artifacts doesn't cover functionality yet, `do` is a fallback
   4. Show env code
     1. decorator approach
     2. Base image is once again from universe, emphasize that you are calling out to a go env from a python env
3. Platform engineer
   1. Run `dagger checks`, show all the checks we saw individually running at once now
     1. NOTE: `integ-test` should NOT be there yet!
   2. Run `dagger artifacts`, show all artifacts collected together
   3. Run `dagger shell dev`
     1. client is in there
     2. run `ps aux`, no server running
     3. but then run `client http://server:8081/hello` but it doesn't work! There's a mismatch between url path, not caught by unit tests
   4. Show env code
     1. Show dev shell, server running as service dependency, cobbling together server + client artifacts thanks to custom codegen
     2. Fix the code in the client or server
     3. Show `dagger shell dev` again, with working command now
     4. Now show example of adding new integ-test that tests the command you were running in the dev shell
     5. Show `dagger checks integ-test` now passing
4. Misc (not fit into above yet)
   1. `dagger codegen`, can show adding a new api to an env and then getting the updated bindings
