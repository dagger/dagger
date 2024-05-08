#/bin/bash

# List the contents of the directory
for item in typescript/*; do
    item=${item:11}
    # Move each item to the temporary directory
    mkdir "$item/typescript"
    mv "typescript/$item/index.ts" "$item/typescript/"
done