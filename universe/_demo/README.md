# CLI Experience
1. Show server code+tests, client code+tests
1. Run `dagger checks`
   1. oops, one failed in the go unit tests
   1. fix the code
   1. re-run `dagger checks`, it passes now
1. Run `dagger artifact list`
   1. run `dagger artifact export --output /tmp/server binary`, show server bin
1. Run `dagger do --help`
   1. Run `dagger do publish --version v1.2.3`
1. Run `dagger shell dev`
   1. run `./client`
   1. oops, it didn't work!
   1. fix the python code
   1. run `dagger shell dev` again, this time `./client` works!
1. Now we want to add an integration test that covers this, how would we go about that?
   1. First, let's go through how all the env entrypoints are defined

# Scripting Experience
1. Show server + client env code
   1. Base images are derived from universe envs.
      1. Those envs are callable from both SDKs despite the env itself being written in Go
      1. Explain `Functions` and their purpose
   1. Server env also implements all its checks+artifacts using Go env
      1. Python will have equivalent too, once we added that to universe
1. Show combined CI env code
   1. We have generated bindings for all the entrypoints in both the server+client env code
      1. mention `dagger codegen` and dependencies in `dagger.json`, just a rough prototype of the DX for now
   1. Show how the checks/artifacts/shells/etc. are defined
   1. Now we want to add an integ test for that problem we hit earlier
      1. Just uncomment the integ test code, explain what it's doing
      1. Run `dagger checks` again, show the new integ test running and passing
