#!/bin/sh

cd $1
CLASS=$(/codegen/codegen dagger:generate-module-classname $2)

if ! [ -f composer.json ]; then
  cp -r /codegen/template/* .
  sed -i "s/class Example/class $CLASS/g" ./src/Example.php
  mv ./src/Example.php ./src/$CLASS.php
fi;

