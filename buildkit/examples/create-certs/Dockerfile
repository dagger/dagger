# syntax=docker/dockerfile:1

FROM alpine:edge AS gen
RUN echo "@testing https://dl-cdn.alpinelinux.org/alpine/edge/testing" >> /etc/apk/repositories
RUN apk --update --no-cache add mkcert@testing ca-certificates
WORKDIR /certs
RUN mkdir -p daemon client
ARG SAN=localhost
ARG SAN_CLIENT=client
RUN echo $SAN | tr " " "\n" >SAN
RUN CAROOT=$(pwd) mkcert -cert-file daemon/cert.pem -key-file daemon/key.pem $SAN
RUN CAROOT=$(pwd) mkcert -client -cert-file client/cert.pem -key-file client/key.pem $SAN_CLIENT
RUN cp -f rootCA.pem daemon/ca.pem
RUN cp -f rootCA.pem client/ca.pem
RUN rm -f rootCA.pem rootCA-key.pem

FROM scratch
COPY --from=gen /certs /
