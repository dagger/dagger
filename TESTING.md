Run all go tests against an engine built from local code (with some parallelism):

```console
./hack/dev go test -parallel 4 -v -count=1 ./...
```

Run one go test against an engine built from local code:

```console
./hack/dev go test -v -count=1 -run TestExtensionMount $(pwd)/core/integration/
```

NOTE: when running core integration tests don't specify an individual file; always run with the full core/integration/ package or you will get errors (due to use of an init func in suite_test.go).

Run nodejs test(s):

```console
./hack/dev yarn run test
```
