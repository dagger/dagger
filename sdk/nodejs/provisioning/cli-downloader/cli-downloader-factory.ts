import { CliDownloaderOptions } from "./cli-downloader-options.js"
import { CliDownloader } from "./cli-downloader.js"
import { DefaultCliDownloader } from "./default-cli-downloader.js"
import { WindowsCliDownloader } from "./windows-cli-downloader.js"

const WIN32_PLATFORM = "win32"

export class CliDownloaderFactory {
  static create(
    platform: NodeJS.Platform,
    options: CliDownloaderOptions
  ): CliDownloader {
    switch (platform) {
      case WIN32_PLATFORM:
        return new WindowsCliDownloader(options)
      default:
        return new DefaultCliDownloader(options)
    }
  }
}
