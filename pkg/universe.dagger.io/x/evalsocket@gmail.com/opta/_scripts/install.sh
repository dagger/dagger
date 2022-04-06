#!/usr/bin/env bash
set -xeo pipefail

# Install AWS CLI
ARCH=$(uname -m)
curl -s "https://awscli.amazonaws.com/awscli-exe-linux-${ARCH}-$1.zip" -o awscliv2.zip
unzip awscliv2.zip -x "aws/dist/awscli/examples/*" "aws/dist/docutils/*"
./aws/install
rm -rf awscliv2.zip aws /usr/local/aws-cli/v2/*/dist/aws_completer /usr/local/aws-cli/v2/*/dist/awscli/data/ac.index /usr/local/aws-cli/v2/*/dist/awscli/examples

# Install Helm
curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 && \
	chmod 700 get_helm.sh && \
	./get_helm.sh && \
	rm ./get_helm.sh

# Install Opta Program
curl -fsSL https://docs.opta.dev/install.sh | sh

# Install Kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
