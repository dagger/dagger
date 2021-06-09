package helm

#code: #"""
	# Add the repository
	if [ -n "$HELM_REPO" ]; then
		helm repo add repository "${HELM_REPO}"
		helm repo update
	fi

	# If the chart is a file, then it's the chart name
	# If it's a directly, then it's the contents of the cart
	if [ -f "/helm/chart" ]; then
	    HELM_CHART="repository/$(cat /helm/chart)"
	else
	    HELM_CHART="/helm/chart"
	fi

	OPTS=""
	OPTS="$OPTS --timeout "$HELM_TIMEOUT""
	OPTS="$OPTS --namespace "$KUBE_NAMESPACE""
	[ "$HELM_WAIT" = "true" ] && OPTS="$OPTS --wait"
	[ "$HELM_ATOMIC" = "true" ] && OPTS="$OPTS --atomic"
	[ -f "/helm/values.yaml" ] && OPTS="$OPTS -f /helm/values.yaml"

	# Select the namespace
	kubectl create namespace "$KUBE_NAMESPACE" || true

	case "$HELM_ACTION" in
	    install)
	        helm install $OPTS "$HELM_NAME" "$HELM_CHART"
	    ;;
	    upgrade)
	        helm upgrade $OPTS "$HELM_NAME" "$HELM_CHART"
	    ;;
	    installOrUpgrade)
	        helm upgrade $OPTS --install "$HELM_NAME" "$HELM_CHART"
	    ;;
	    *)
	        echo unsupported helm action "$HELM_ACTION"
	        exit 1
	    ;;
	esac
	"""#
