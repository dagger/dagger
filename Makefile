.PHONY: dagger
dagger:
	go generate ./dagger && go build -o ./cmd/dagger/ ./cmd/dagger/
