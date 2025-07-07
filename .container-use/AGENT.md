This is a Dagger codebase written in Go. Key commands:
- go build ./cmd/dagger - builds the dagger CLI
- go test ./... - runs all tests
- The current branch is poc-default-secrets and contains a partial implementation of .env.dagger.json support
- The next step is to modify core/object.go:installConstructor() to merge .env.dagger.json arguments with constructor calls
- .env.dagger.json contains string values that need to be parsed into proper Dagger types using the ArgumentParser
- Call arguments should take precedence over env arguments