```gql
query {
  host {
    directory(path: ".") {
      file(path: "README.md") {
        contents
      }
    }
  }
}
```
