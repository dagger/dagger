# Claok

## Setup

```console
docker run -d -p 5555:5000 --name registry registry:2
docker build -f ./Dockerfile.ci -t localhost:5555/ci . && docker push localhost:5555/ci
docker build -f ./Dockerfile.netlify -t localhost:5555/netlify . && docker push localhost:5555/netlify
go build ./cmd/cloak
```

## Invoking

```console
./cloak localhost:5555/netlify deploy
./cloak localhost:5555/ci test
./cloak localhost:5555/ci deploy
```

## Stubbing

```console
cd stub
go build
./stub ./example/dagger.cue
```