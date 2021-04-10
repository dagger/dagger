package elb

import (
	"dagger.io/aws"
)

// Returns a non-taken rule priority (randomized)
#RandomRulePriority: {
	// AWS Config
	config: aws.#Config

	// ListenerArn
	listenerArn: string

	// Optional vhost for reusing priorities
	vhost?: string

	aws.#Script & {
		files: {
			"/inputs/listenerArn":    listenerArn
			if vhost != _|_ {
				"/inputs/vhost": vhost
			}
		}

		export: "/priority"

		//FIXME: The code below can end up not finding an available prio
		// Better to exclude the existing allocated priorities from the random sequence
		code: #"""
			if [ -s /inputs/vhost ]; then
				# We passed a vhost as input, try to recycle priority from previously allocated vhost
				vhost="$(cat /inputs/vhost)"

				priority=$(aws elbv2 describe-rules \
					--listener-arn "$(cat /inputs/listenerArn)" | \
					jq -r --arg vhost "$vhost" '.Rules[] | select(.Conditions[].HostHeaderConfig.Values[] == $vhost) | .Priority')

				if [ -n "${priority}" ]; then
					echo -n "${priority}" > /priority
					exit 0
				fi
			fi

			# Grab a priority random from 1-50k and check if available, retry 10 times if none available
			priority=0
			for i in {1..10}
			do
				p=$(shuf -i 1-50000 -n 1)
				# Find the next priority available that we can allocate
				aws elbv2 describe-rules \
					--listener-arn "$(cat /inputs/listenerArn)" \
					| jq -e "select(.Rules[].Priority == \"${p}\") | true" && continue
				priority="${p}"
				break
			done
			if [ "${priority}" -lt 1 ]; then
				echo "Error: cannot determine a Rule priority"
				exit 1
			fi
			echo -n "${priority}" > /priority
			"""#
	}
}
