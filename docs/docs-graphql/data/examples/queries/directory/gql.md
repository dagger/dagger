```gql
query {
  directory {
    withNewDirectory(path: "foo") {
      withNewDirectory(path: "foo/bar/baz") {
        withNewFile(path: "foo/bar/greeting", contents: "hello, world!\n") {
          foo: entries(path: "foo")
          bar: entries(path: "foo/bar")
          greeting: file(path: "foo/bar/greeting") {
            contents
          }
        }
      }
    }
  }
}
```
