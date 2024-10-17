#!/bin/bash

# Make sure not to load any implicit module
cd $(mktemp -d)
# Run a simple query to "warm up" the engine
echo '{directory{id}}' | dagger query
