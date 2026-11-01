package dagger

// go-client-dev's generate runs this command on a schema file, with
// dagger-go-sdk-codegen built at the github.com/dagger/go-sdk commit it pins.
// cmd/codegen generate-library remains only to compare against and to roll
// back to.
//go:generate dagger-go-sdk-codegen core --output ./core --dag-output ./dag
