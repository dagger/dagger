// AWS Elastic Load Balancer (ELBv2)
package elb

import (
	"dagger.io/dagger/op"
	"dagger.io/aws"
)

// Returns an unused rule priority (randomized in available range)
#RandomRulePriority: {

	// AWS Config
	config: aws.#Config

	// ListenerArn
	listenerArn: string @dagger(input)

	// Optional vhost for reusing priorities
	vhost?: string @dagger(input)

	// exported priority
	priority: out @dagger(output)

	out: {
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
			},

			op.#Exec & {
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					#"""
						if [ -s "$VHOST" ]; then
							# We passed a vhost as input, try to recycle priority from previously allocated vhost
							priority=$(aws elbv2 describe-rules \
								--listener-arn "$LISTENER_ARN" | \
								jq -r --arg vhost "$VHOST" '.Rules[] | select(.Conditions[].HostHeaderConfig.Values[] == $VHOST) | .Priority')
						
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
								--listener-arn "$LISTENER_ARN" \
								| jq -e "select(.Rules[].Priority == \"${p}\") | true" && continue
							priority="${p}"
							break
						done
						if [ "${priority}" -lt 1 ]; then
							echo "Error: cannot determine a Rule priority"
							exit 1
						fi
						echo -n "${priority}" > /priority
						"""#,
				]
				env: {
					LISTENER_ARN: listenerArn
					VHOST:        vhost
				}
			},

			op.#Export & {
				source: "/db_created"
				format: "string"
			},
		]
	}
}
