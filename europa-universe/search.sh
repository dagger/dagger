#!/bin/bash

find . -name '*.cue' -exec grep -H "$1" {} \;
