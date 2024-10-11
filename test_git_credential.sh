#!/bin/bash

# debug concurrent run on Git

run_git_credential() {
    echo "Run $1:"
    echo -e "protocol=https\nhost=github.com\n" | git credential fill
    echo "---"
}

for i in {1..10}; do
    run_git_credential $i &
done

wait
