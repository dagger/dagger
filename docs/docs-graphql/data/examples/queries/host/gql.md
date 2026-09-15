```gql
query {
  host {
    read: directory(path: ".") {
      file(path: ".markdownlint.yaml") {
        contents
      }
    }
    home: envVariable(name: "HOME") {
      value
    }
    pwd: envVariable(name: "PWD") {
      value
    }
    write: directory(path: ".") {
      withNewFile(path: "greeting", contents: "Hello Dagger!") {
        export(path: ".")
      }
    }
  }
}
```
