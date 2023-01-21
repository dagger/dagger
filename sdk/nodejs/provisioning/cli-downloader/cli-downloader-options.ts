export interface CliDownloaderOptions {
  cliVersion: string
  archive?: {
    checksumUrl?: string
    url?: string
    name?(architecture: string): string
    extract?(archivePath: string, destinationPath: string): void
    path?(destinationFolder: string): string
  }
  executableFilename?(name: string): string
}
