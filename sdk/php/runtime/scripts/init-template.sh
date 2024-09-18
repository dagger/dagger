#!/bin/sh

set -eux

CLASS=$(/sdk/scripts/codegen.php dagger:generate-module-classname "$1")

if ! [ -f composer.json ]; then
    cp -r /opt/template/* .
    sed -i "s/Example/$CLASS/g" ./src/Example.php
    mv ./src/Example.php ./src/$CLASS.php

    PACKAGE_NAME=$(echo $1 | tr '[:upper:]' '[:lower:]' | tr -s '[:blank:]' '\-')

    sed -i "s/daggermodule\/example/daggermodule\/$PACKAGE_NAME/g" ./composer.json
fi
