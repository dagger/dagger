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

<a href="https://play.dagger.cloud/playground/Ai-Fnu3Q4vo" target="_blank">Try it in the API Playground!</a>
