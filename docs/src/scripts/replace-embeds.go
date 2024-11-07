package main

import (
	"fmt"
	"os"

	"strings"
)

// Define the struct to hold the data
type FileEntry struct {
	SourceFilename string
	Language       string
	EmbedID        string
	ScriptFilename string
}

// Function to process each file entry
func processFile(entry FileEntry) error {
	// Read the contents of the source file
	content, err := os.ReadFile(entry.SourceFilename)
	if err != nil {
		return fmt.Errorf("error reading file %s: %w", entry.SourceFilename, err)
	}

	// Replace occurrences of ID with script filename
	// HACK: do this twice because some <Embed tags have whitespace before closing and some don't
	// handle <Embed .. /> (with spaces)
	modifiedContent := strings.ReplaceAll(string(content), "<Embed id=\""+entry.EmbedID+"\" />", "```"+entry.Language+" file="+entry.ScriptFilename+"\r```")
	// handle <Embed ../> (without spaces)
	modifiedContent = strings.ReplaceAll(modifiedContent, "<Embed id=\""+entry.EmbedID+"\"/>", "```"+entry.Language+" file="+entry.ScriptFilename+"\r```")

	// Write the modified content back to the source file
	err = os.WriteFile(entry.SourceFilename, []byte(modifiedContent), 0644)
	if err != nil {
		return fmt.Errorf("error writing to file %s: %w", entry.SourceFilename, err)
	}

	fmt.Printf("Processed file: %s (Replaced '%s' with '%s')\n", entry.SourceFilename, entry.EmbedID, entry.ScriptFilename)
	return nil
}

func main() {
	// Define multiple file entries
	entries := []FileEntry{
		{
			SourceFilename: "current_docs/quickstart/593914-hello.mdx",
			Language:       "go",
			EmbedID:        "AqF1QrJBh1L",
			ScriptFilename: "./snippets/hello/main.go",
		},
		{
			SourceFilename: "current_docs/quickstart/593914-hello.mdx",
			Language:       "javascript",
			EmbedID:        "ANSxtgC136o",
			ScriptFilename: "./snippets/hello/index.mjs",
		},
		{
			SourceFilename: "current_docs/quickstart/593914-hello.mdx",
			Language:       "python",
			EmbedID:        "sLUm92wLwtw",
			ScriptFilename: "./snippets/hello/main.py",
		},
		{
			SourceFilename: "current_docs/quickstart/947391-test.mdx",
			Language:       "go",
			EmbedID:        "v676qm2pxae",
			ScriptFilename: "./snippets/test/main.go",
		},
		{
			SourceFilename: "current_docs/quickstart/947391-test.mdx",
			Language:       "javascript",
			EmbedID:        "Wb_vszr9Ule",
			ScriptFilename: "./snippets/test/index.mjs",
		},
		{
			SourceFilename: "current_docs/quickstart/947391-test.mdx",
			Language:       "python",
			EmbedID:        "yZj9P2N03-D",
			ScriptFilename: "./snippets/test/main.py",
		},
		{
			SourceFilename: "current_docs/quickstart/349011-build.mdx",
			Language:       "go",
			EmbedID:        "smjo0gkeiFz",
			ScriptFilename: "./snippets/build/main.go",
		},
		{
			SourceFilename: "current_docs/quickstart/349011-build.mdx",
			Language:       "javascript",
			EmbedID:        "5i8mcHTVJ-x",
			ScriptFilename: "./snippets/build/index.mjs",
		},
		{
			SourceFilename: "current_docs/quickstart/349011-build.mdx",
			Language:       "python",
			EmbedID:        "E8rjb5DFd2m",
			ScriptFilename: "./snippets/build/main.py",
		},
		{
			SourceFilename: "current_docs/quickstart/730264-publish.mdx",
			Language:       "go",
			EmbedID:        "PgPJUlg4Aq8",
			ScriptFilename: "./snippets/publish/main.go",
		},
		{
			SourceFilename: "current_docs/quickstart/730264-publish.mdx",
			Language:       "javascript",
			EmbedID:        "1P5-90wivSP",
			ScriptFilename: "./snippets/publish/index.mjs",
		},
		{
			SourceFilename: "current_docs/quickstart/730264-publish.mdx",
			Language:       "python",
			EmbedID:        "8vUQnWJ6mxe",
			ScriptFilename: "./snippets/publish/main.py",
		},
		{
			SourceFilename: "current_docs/quickstart/472910-build-multi.mdx",
			Language:       "go",
			EmbedID:        "NJF-IQ7XzG-",
			ScriptFilename: "./snippets/build-multi/main.go",
		},
		{
			SourceFilename: "current_docs/quickstart/472910-build-multi.mdx",
			Language:       "javascript",
			EmbedID:        "loDAHfMcysK",
			ScriptFilename: "./snippets/build-multi/index.mjs",
		},
		{
			SourceFilename: "current_docs/quickstart/472910-build-multi.mdx",
			Language:       "python",
			EmbedID:        "3xJIBUQdEkp",
			ScriptFilename: "./snippets/build-multi/main.py",
		},
		{
			SourceFilename: "current_docs/quickstart/429462-build-dockerfile.mdx",
			Language:       "go",
			EmbedID:        "gFFeUi06wW3",
			ScriptFilename: "./snippets/build-dockerfile/main.go",
		},
		{
			SourceFilename: "current_docs/quickstart/429462-build-dockerfile.mdx",
			Language:       "javascript",
			EmbedID:        "AnwtJTV9Xj3",
			ScriptFilename: "./snippets/build-dockerfile/index.mjs",
		},
		{
			SourceFilename: "current_docs/quickstart/429462-build-dockerfile.mdx",
			Language:       "python",
			EmbedID:        "EaSvAH9iPiG",
			ScriptFilename: "./snippets/build-dockerfile/main.py",
		},
		{
			SourceFilename: "current_docs/quickstart/635927-caching.mdx",
			Language:       "go",
			EmbedID:        "1J5Fs4NoQcW",
			ScriptFilename: "./snippets/caching/main.go",
		},
		{
			SourceFilename: "current_docs/quickstart/635927-caching.mdx",
			Language:       "javascript",
			EmbedID:        "HjFX8td15TX",
			ScriptFilename: "./snippets/caching/index.mjs",
		},
		{
			SourceFilename: "current_docs/quickstart/635927-caching.mdx",
			Language:       "python",
			EmbedID:        "TCdOYrKMfV_d",
			ScriptFilename: "./snippets/caching/main.py",
		},
		{
			SourceFilename: "current_docs/api/975146-concepts.mdx",
			Language:       "graphql",
			EmbedID:        "9SB8ePzCltX",
			ScriptFilename: "./snippets/concepts/query1.gql",
		},
		{
			SourceFilename: "current_docs/api/975146-concepts.mdx",
			Language:       "graphql",
			EmbedID:        "8I0c_SMKQbj",
			ScriptFilename: "./snippets/concepts/query2.gql",
		},
		{
			SourceFilename: "current_docs/api/975146-concepts.mdx",
			Language:       "graphql",
			EmbedID:        "PE1GJrfZODq",
			ScriptFilename: "./snippets/concepts/query3.gql",
		},
		{
			SourceFilename: "current_docs/api/975146-concepts.mdx",
			Language:       "graphql",
			EmbedID:        "DKUH7PI5Yt0",
			ScriptFilename: "./snippets/concepts/query4.gql",
		},
		{
			SourceFilename: "current_docs/api/975146-concepts.mdx",
			Language:       "graphql",
			EmbedID:        "NuLZcSHaNno",
			ScriptFilename: "./snippets/concepts/query5.gql",
		},
		{
			SourceFilename: "current_docs/api/975146-concepts.mdx",
			Language:       "graphql",
			EmbedID:        "SLtXQ4lvqNS",
			ScriptFilename: "./snippets/concepts/query6.gql",
		},
	}

	// Iterate over the entries and process each file
	for _, entry := range entries {
		err := processFile(entry)
		if err != nil {
			fmt.Println("Error:", err)
		}
	}
}
