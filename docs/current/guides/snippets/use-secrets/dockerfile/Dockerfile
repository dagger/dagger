FROM alpine:3.17
RUN apk add curl
RUN --mount=type=secret,id=gh-secret curl "https://api.github.com/repos/dagger/dagger/issues" --header "Accept: application/vnd.github+json" --header "Authorization: Bearer $(cat /run/secrets/gh-secret)"
