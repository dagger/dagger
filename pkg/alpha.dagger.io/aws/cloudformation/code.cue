package cloudformation

#Code: #"""
	set +o pipefail

	aws cloudformation validate-template --template-body file:///src/template.json
	parameters=""

	function getOutputs() {
	    aws cloudformation describe-stacks \
	        --stack-name "$STACK_NAME" \
	        --query 'Stacks[].Outputs' \
	        --output json \
	        | jq '.[] | map( { (.OutputKey|tostring): .OutputValue } ) | add' \
	        > /outputs.json
	}

	# Check if the stack exists
	aws cloudformation describe-stacks --stack-name "$STACK_NAME" 2>/dev/null || {
	    if [ -f /src/parameters.json ]; then
	        parameters="--parameters file:///src/parameters.json"
	        cat /src/parameters.json
	    fi

	    aws cloudformation create-stack \
	        --stack-name "$STACK_NAME" \
	        --template-body "file:///src/template.json" \
	        --capabilities CAPABILITY_IAM \
	        --on-failure "$ON_FAILURE" \
	        --timeout-in-minutes "$TIMEOUT" \
	        $parameters \
	    || {
	        # Create failed, display errors
	        aws cloudformation describe-stack-events \
	            --stack-name "$STACK_NAME" \
	            --max-items 10 \
	            | >&2 jq '.StackEvents[] | select((.ResourceStatus | contains("FAILED")) or (.ResourceStatus | contains("ERROR"))) | ("===> ERROR: " + .LogicalResourceId + ": " + .ResourceStatusReason)'
	        exit 1
	    }

	    aws cloudformation wait stack-create-complete \
	        --stack-name "$STACK_NAME"

	    getOutputs
	    exit 0
	}

	# In case there is an action already in progress, we wait for the corresponding action to complete
	wait_action=""
	stack_status=$(aws cloudformation describe-stacks --stack-name "$STACK_NAME" | jq -r '.Stacks[].StackStatus')
	case "$stack_status" in
	    "CREATE_FAILED")
	        echo "Deleting previous failed stack..."
	        aws cloudformation delete-stack --stack-name "$STACK_NAME"
	        aws cloudformation wait stack-delete-complete --stack-name "$STACK_NAME" || true
	        ;;
	    "CREATE_IN_PROGRESS")
	        echo "Stack create already in progress, waiting..."
	        aws cloudformation wait stack-create-complete --stack-name "$STACK_NAME" || true
	        ;;
	    "UPDATE_IN_PROGRESS")
	        # Cancel update to avoid stacks stuck in deadlock (re-apply then works)
	        echo "Stack update already in progress, waiting..."
	        aws cloudformation cancel-update-stack --stack-name "$STACK_NAME" || true
	        ;;
	    "ROLLBACK_IN_PROGRESS")
	        echo "Stack rollback already in progress, waiting..."
	        aws cloudformation wait stack-rollback-complete --stack-name "$STACK_NAME" || true
	        ;;
	    "DELETE_IN_PROGRESS")
	        echo "Stack delete already in progress, waiting..."
	        aws cloudformation wait stack-delete-complete --stack-name "$STACK_NAME" || true
	        ;;
	    "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS")
	        echo "Stack update almost completed, waiting..."
	        aws cloudformation wait stack-update-complete --stack-name "$STACK_NAME" || true
	        ;;
	esac

	[ -n "$NEVER_UPDATE" ] && {
	    getOutputs
	    exit 0
	}

	# Stack exists, trigger an update via `deploy`
	if [ -f /src/parameters_overrides.json ]; then
	    parameters="--parameter-overrides file:///src/parameters_overrides.json"
	    cat /src/parameters_overrides.json
	fi
	echo "Deploying stack $STACK_NAME"
	aws cloudformation deploy \
	    --stack-name "$STACK_NAME" \
	    --template-file "/src/template.json" \
	    --capabilities CAPABILITY_IAM \
	    --no-fail-on-empty-changeset \
	    $parameters \
	|| {
	    # Deploy failed, display errors
	    echo "Failed to deploy stack $STACK_NAME"
	    aws cloudformation describe-stack-events \
	        --stack-name "$STACK_NAME" \
	        --max-items 10 \
	        | >&2 jq '.StackEvents[] | select((.ResourceStatus | contains("FAILED")) or (.ResourceStatus | contains("ERROR"))) | ("===> ERROR: " + .LogicalResourceId + ": " + .ResourceStatusReason)'
	    exit 1
	}

	getOutputs
	"""#
