```gql
query {
  container {
    from(address: "alpine") {
      directory(path: ".") {
        withNewDirectory(path: "foo") {
          withNewDirectory(path: "foo/bar/baz") {
            withNewFile(path: "foo/bar/greeting", contents: "hello, world!\n") {
              entries(path: "foo/bar")
              file(path: "foo/bar/greeting") {
                contents
              }
            }
          }
        }
      }
    }
  }
}
```
