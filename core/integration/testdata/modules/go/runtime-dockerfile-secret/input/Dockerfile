FROM alpine:3.22.1
RUN --mount=type=secret,id=my-secret test "$(cat /run/secrets/my-secret)" = "barbar"
