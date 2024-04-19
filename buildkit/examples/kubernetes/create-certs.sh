#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail
set -o errtrace

PRODUCT=buildkit
DIR=./.certs
if [[ "$#" -lt 1 ]]; then
	echo "Usage: $0 SAN [SAN...]"
	echo
	echo "Example: $0 buildkitd.default.svc 127.0.0.1"
	echo
	echo "The following files will be created under ${DIR}"
	echo "- daemon/{ca.pem,cert.pem,key.pem}"
	echo "- client/{ca.pem,cert.pem,key.pem}"
	echo "- ${PRODUCT}-daemon-certs.yaml"
	echo "- ${PRODUCT}-client-certs.yaml"
	echo "- SAN"
	exit 1
fi
if ! command -v mkcert >/dev/null; then
	echo "Missing mkcert (https://github.com/FiloSottile/mkcert)"
	exit 1
fi
SAN=$@
SAN_CLIENT=client

mkdir -p $DIR ${DIR}/daemon ${DIR}/client
(
	cd $DIR
	echo $SAN | tr " " "\n" >SAN
	CAROOT=$(pwd) mkcert -cert-file daemon/cert.pem -key-file daemon/key.pem ${SAN} >/dev/null 2>&1
	CAROOT=$(pwd) mkcert -client -cert-file client/cert.pem -key-file client/key.pem ${SAN_CLIENT} >/dev/null 2>&1
	cp -f rootCA.pem daemon/ca.pem
	cp -f rootCA.pem client/ca.pem
	rm -f rootCA.pem rootCA-key.pem

	kubectl create secret generic ${PRODUCT}-daemon-certs --dry-run=client -o yaml --from-file=./daemon >${PRODUCT}-daemon-certs.yaml
	kubectl create secret generic ${PRODUCT}-client-certs --dry-run=client -o yaml --from-file=./client >${PRODUCT}-client-certs.yaml
)
