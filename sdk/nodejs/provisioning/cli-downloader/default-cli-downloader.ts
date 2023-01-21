import { CliDownloaderOptions } from "./cli-downloader-options.js"
import { CliDownloader } from "./cli-downloader.js"

export class DefaultCliDownloader extends CliDownloader {
  constructor(options: CliDownloaderOptions) {
    super(options)
  }
}
