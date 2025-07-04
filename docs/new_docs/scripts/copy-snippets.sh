#!/bin/bash

# Script to copy snippets from current_docs to new_docs while converting to .mdx
# Usage: ./copy-snippets.sh

# Define source and destination roots
SOURCE_ROOT="/Users/jason/go/src/github.com/jasonmccallister/dagger/docs/current_docs"
DEST_ROOT="/Users/jason/go/src/github.com/jasonmccallister/dagger/docs/new_docs/snippets/code"

# Create destination directory if it doesn't exist
mkdir -p "$DEST_ROOT"

echo "Searching for snippet files in: $SOURCE_ROOT"
echo "Destination directory: $DEST_ROOT"

# Find all snippet files in the current_docs directory
FOUND_FILES=$(find "$SOURCE_ROOT" -type f -path "*/snippets/*")
if [ -z "$FOUND_FILES" ]; then
    echo "No snippet files found. Check if the path is correct."
    exit 1
fi

echo "Found $(echo "$FOUND_FILES" | wc -l | tr -d ' ') snippet files."

find "$SOURCE_ROOT" -type f -path "*/snippets/*" | while read -r source_file; do
    # Extract the relative path from SOURCE_ROOT
    rel_path="${source_file#$SOURCE_ROOT/}"
    
    # Extract the directory part and the filename
    dir_part=$(dirname "$rel_path")
    filename=$(basename "$source_file")
    
    # Replace the period in the extension with a hyphen
    # For example: dagger.json -> dagger-json.mdx
    new_filename=$(echo "$filename" | sed 's/\./\-/g')
    
    # Create the destination directory structure
    dest_dir="$DEST_ROOT/${dir_part#*/}"
    mkdir -p "$dest_dir"
    
    # Define the destination file path with hyphen-separated extension + .mdx
    dest_file="$dest_dir/$new_filename.mdx"
    
    # Determine language and icon for code block
    extension="${filename##*.}"
    case "$extension" in
        go)
            lang="go Go icon=\"golang\" wrap"
            ;;
        ts|js)
            lang="typescript Typescript icon=\"javascript\" wrap"
            ;;
        php)
            lang="php PHP icon=\"php\" wrap"
            ;;
        java)
            lang="java Java icon=\"java\" wrap"
            ;;
        py)
            lang="python Python icon=\"python\" wrap"
            ;;
        *)
            lang=""
            ;;
    esac
    
    # Wrap the content in triple backtick code blocks with language and icon if set
    if [ -n "$lang" ]; then
        echo "\`\`\`$lang" > "$dest_file"
    else
        echo "\`\`\`" > "$dest_file"
    fi
    cat "$source_file" >> "$dest_file"
    echo "\`\`\`" >> "$dest_file"
    
    echo "Copied $source_file to $dest_file"
done

echo "Snippet conversion complete!"
