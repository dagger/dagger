#!/bin/sh

cd $1

if ! [ -f composer.json ]; then
  cp -r /codegen/template/* .
fi;