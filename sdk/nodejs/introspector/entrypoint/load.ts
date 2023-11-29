export async function load(files: string[]): Promise<void> {
  await Promise.all(files.map(async (f) => await import(f)))
}
