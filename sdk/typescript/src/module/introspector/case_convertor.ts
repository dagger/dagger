export function convertToPascalCase(input: string): string {
  const words = input
    .replace(/[^a-zA-Z0-9]/g, " ") // Replace non-alphanumeric characters with spaces
    .split(/\s+/)
    .filter((word) => word.length > 0)

  if (words.length === 0) {
    return "" // No valid words found
  }

  // It's an edge case when moduleName is already in PascalCase or camelCase
  if (words.length === 1) {
    return words[0].charAt(0).toUpperCase() + words[0].slice(1)
  }

  const pascalCase = words
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join("")

  return pascalCase
}
