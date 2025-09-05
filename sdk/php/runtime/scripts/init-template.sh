#!/bin/sh

set -eux

CLASS=$(/sdk/scripts/codegen.php dagger:generate-module-classname "$1")

if ! [ -f composer.json ]; then
    cp -r /opt/template/* .
    sed -i "s/Example/$CLASS/g" ./src/Example.php
    mv ./src/Example.php ./src/$CLASS.php

    PHP_VERSION=${PHP_VERSION:?}
    PACKAGE_NAME=$(echo $1 | tr '[:upper:]' '[:lower:]' | tr -s '[:blank:]' '\-')

    PHP_MAJOR_MINOR=$(printf '%s\n' "$PHP_VERSION" | sed -E 's/^([0-9]+)\.([0-9]+).*/\1.\2/');
    PHP_CONSTRAINT="^${PHP_MAJOR_MINOR}";

    tmpfile=$(mktemp);
    jq \
      --arg pkg "daggermodule/${PACKAGE_NAME}" \
      --arg phpversion "${PHP_VERSION}" \
      --arg phpconstraint "${PHP_CONSTRAINT}" \
      '.name = $pkg
       | .config.platform.php = $phpversion
       | .require.php = $phpconstraint' ./composer.json \
    > "${tmpfile}" \
    && mv "${tmpfile}" ./composer.json;
fi
