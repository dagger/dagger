export function convertToPascalCase(input: string): string {
  // Handle empty string case
  if (!input) {
    return ""
  }

  // Split on word boundaries before uppercase letters, numbers, and special characters
  const words = input
    .split(/(?=[A-Z0-9])|[^a-zA-Z0-9]|(?<=[a-zA-Z])(?=\d)|(?<=\d)(?=[a-zA-Z])/g)
    .filter((word) => word.length > 0)

  // Convert each word to proper case
  const pascalCase = words
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join("")

  return pascalCase
}
