#!/bin/sh

if [ -f /run/secrets/accessKeyId ]; then
	export AWS_ACCESS_KEY_ID=$(cat /run/secrets/accessKeyId)
fi

if [ -f /run/secrets/secretAccessKey ]; then
	export AWS_SECRET_ACCESS_KEY=$(cat /run/secrets/secretAccessKey)
fi

if [ -f /run/secrets/sessionToken ]; then
	export AWS_SESSION_TOKEN=$(cat /run/secrets/sessionToken)
fi