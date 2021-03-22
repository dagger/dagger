package eks

#Code: #"""
	[ -e /cache/bin/kubectl ] || {
	    curl -sfL https://dl.k8s.io/${KUBECTL_VERSION}/bin/linux/amd64/kubectl -o /cache/bin/kubectl \
		&& chmod +x /cache/bin/kubectl
	}

	export KUBECONFIG=/kubeconfig
	export PATH="$PATH:/cache/bin"

	# Generate a kube configuration
	aws eks update-kubeconfig --name "$EKS_CLUSTER"

	# Figure out the kubernetes username
	CONTEXT="$(kubectl config current-context)"
	USER="$(kubectl config view -o json | \
	    jq -r ".contexts[] | select(.name==\"$CONTEXT\") | .context.user")"

	# Grab a kubernetes access token
	ACCESS_TOKEN="$(aws eks get-token --cluster-name "$EKS_CLUSTER" | \
	    jq -r .status.token)"

	# Remove the user config and replace it with the token
	kubectl config unset "users.${USER}"
	kubectl config set-credentials "$USER" --token "$ACCESS_TOKEN"
	"""#
