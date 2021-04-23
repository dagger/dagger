package netlify

#code: #"""
	create_site() {
	    url="https://api.netlify.com/api/v1/${NETLIFY_ACCOUNT:-}/sites"

	    response=$(curl -s -S -f -H "Authorization: Bearer $NETLIFY_AUTH_TOKEN" \
	                -X POST -H "Content-Type: application/json" \
	                $url \
	                -d "{\"name\": \"${NETLIFY_SITE_NAME}\", \"custom_domain\": \"${NETLIFY_DOMAIN}\"}"
	            )
	    if [ $? -ne 0 ]; then
	        exit 1
	    fi

	    echo $response | jq -r '.site_id'
	}

	site_id=$(curl -s -S -f -H "Authorization: Bearer $NETLIFY_AUTH_TOKEN" \
	            https://api.netlify.com/api/v1/sites\?filter\=all | \
	            jq -r ".[] | select(.name==\"$NETLIFY_SITE_NAME\") | .id" \
	        )
	if [ -z "$site_id" ] ; then
	    if [ "${NETLIFY_SITE_CREATE:-}" != 1 ]; then
	        echo "Site $NETLIFY_SITE_NAME does not exist"
	        exit 1
	    fi
	    site_id=$(create_site)
	    if [ -z "$site_id" ]; then
	        echo "create site failed"
	        exit 1
	    fi
	fi
	netlify deploy \
	    --dir="$(pwd)" \
	    --site="$site_id" \
	    --prod \
	| tee /tmp/stdout

	url=$(</tmp/stdout sed -n -e 's/^Website URL:.*\(https:\/\/.*\)$/\1/p' | tr -d '\n')
	deployUrl=$(</tmp/stdout sed -n -e 's/^Unique Deploy URL:.*\(https:\/\/.*\)$/\1/p' | tr -d '\n')
	logsUrl=$(</tmp/stdout sed -n -e 's/^Logs:.*\(https:\/\/.*\)$/\1/p' | tr -d '\n')

	jq -n \
		--arg siteId "$site_id" \
		--arg url "$url" \
		--arg deployUrl "$deployUrl" \
		--arg logsUrl "$logsUrl" \
		'{siteId: $siteId, url: $url, deployUrl: $deployUrl, logsUrl: $logsUrl}' > /output.json
	"""#
