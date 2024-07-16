#!/bin/sh

cd $1
CLASS=$(/codegen/entrypoint.php dagger:generate-module-classname "$2")

if ! [ -f composer.json ]; then
  cp -r /codegen/template/* .
  sed -i "s/class Example/class $CLASS/g" ./src/Example.php
  mv ./src/Example.php ./src/$CLASS.php

  PACKAGE_NAME=$(echo $2 | tr '[:upper:]' '[:lower:]'| tr -s '[:blank:]' '\-')

  sed -i "s/daggermodule\/example/daggermodule\/$PACKAGE_NAME/g" ./composer.json
fi;
