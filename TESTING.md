Run all go tests (with some parallelism):

```console
go test -parallel 4 -v -count=1 ./...
```

Run one go test:

```console
go test -v -count=1 -run TestExtensionMount $(pwd)/core/integration/
```

NOTE: when running core integration tests don't specify an individual file; always run with the full core/integration/ package or you will get errors (due to use of an init func in suite_test.go).

Run nodejs test(s):

```console
yarn run test
```
