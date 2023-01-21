import assert from "assert"

import { CliDownloaderFactory } from "../../provisioning/cli-downloader/cli-downloader-factory.js"
import { DefaultCliDownloader } from "../../provisioning/cli-downloader/default-cli-downloader.js"
import { WindowsCliDownloader } from "../../provisioning/cli-downloader/windows-cli-downloader.js"
import { CLI_VERSION } from "../../provisioning/default.js"

describe("CliDownloaderFactory", () => {
  describe("create()", () => {
    describe("when os is windows", () => {
      it("it uses WindowsCliDownloader", () => {
        const cliDownloader = CliDownloaderFactory.create("win32", {
          cliVersion: CLI_VERSION,
        })
        assert(cliDownloader instanceof WindowsCliDownloader)
      })
    })

    describe("when os is not windows", () => {
      it("it uses DefaultCliDownloader", () => {
        const cliDownloader = CliDownloaderFactory.create("linux", {
          cliVersion: CLI_VERSION,
        })
        assert(cliDownloader instanceof DefaultCliDownloader)
      })
    })
  })
})
