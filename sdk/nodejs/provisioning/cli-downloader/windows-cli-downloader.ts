import AdmZip from "adm-zip"
import * as path from "path"

import { CliDownloaderOptions } from "./cli-downloader-options.js"
import { CliDownloader, DAGGER_CLI_BIN_PREFIX } from "./cli-downloader.js"

export class WindowsCliDownloader extends CliDownloader {
  constructor(
    options: Omit<CliDownloaderOptions, "archive" | "executableFilename">
  ) {
    super({
      ...options,
      executableFilename(name) {
        return `${name}.exe`
      },
      archive: {
        extract(archivePath, destinationPath) {
          const zip = new AdmZip(archivePath)
          zip.extractEntryTo(
            `${DAGGER_CLI_BIN_PREFIX}.exe`,
            destinationPath,
            false,
            true
          )
        },
        name(architecture) {
          return `${DAGGER_CLI_BIN_PREFIX}_v${options.cliVersion}_windows_${architecture}.zip`
        },
        path(destinationFolder) {
          return path.join(destinationFolder, `${DAGGER_CLI_BIN_PREFIX}.zip`)
        },
      },
    })
  }
}
