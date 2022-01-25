#!/bin/sh
curl -sL https://alpine-pkgs.sgerrand.com/sgerrand.rsa.pub -o /etc/apk/keys/sgerrand.rsa.pub 
curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-2.31-r0.apk 
curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-bin-2.31-r0.apk 
curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-i18n-2.31-r0.apk 
apk add --no-cache glibc-2.31-r0.apk glibc-bin-2.31-r0.apk glibc-i18n-2.31-r0.apk 
/usr/glibc-compat/bin/localedef -i en_US -f UTF-8 en_US.UTF-8 
ARCH=$(uname -m)
curl -s "https://awscli.amazonaws.com/awscli-exe-linux-${ARCH}-$1.zip" -o awscliv2.zip
unzip awscliv2.zip
./aws/install
rm -rf awscliv2.zip aws /usr/local/aws-cli/v2/*/dist/aws_completer /usr/local/aws-cli/v2/*/dist/awscli/data/ac.index /usr/local/aws-cli/v2/*/dist/awscli/examples glibc-*.apk