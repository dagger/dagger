#!/bin/bash

find . -name '*.cue' -exec cue fmt -s {} \;
