#!/bin/sh

cd $1
CLASS="$(echo -n ${2:0:1} | tr '[:lower:]' '[:upper:]')${2:1}"

if ! [ -f composer.json ]; then
  cp -r /codegen/template/* .
  sed -i "s/class Example/class $CLASS/g" ./src/Example.php
  mv ./src/Example.php ./src/$CLASS.php
fi;

