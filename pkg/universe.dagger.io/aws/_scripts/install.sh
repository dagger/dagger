#!/bin/sh
ARCH=$(uname -m)
curl -s "https://awscli.amazonaws.com/awscli-exe-linux-${ARCH}-$1.zip" -o awscliv2.zip
unzip awscliv2.zip
./aws/install
rm -rf awscliv2.zip aws /usr/local/aws-cli/v2/*/dist/aws_completer /usr/local/aws-cli/v2/*/dist/awscli/data/ac.index /usr/local/aws-cli/v2/*/dist/awscli/examples