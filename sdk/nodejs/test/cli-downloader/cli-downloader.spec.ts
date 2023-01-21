import assert from "assert"
import * as os from "os"

import { InitEngineSessionBinaryError } from "../../common/errors/InitEngineSessionBinaryError.js"
import { CliDownloaderFactory } from "../../provisioning/cli-downloader/cli-downloader-factory.js"
import { CliDownloaderOptions } from "../../provisioning/cli-downloader/cli-downloader-options.js"
import { CLI_VERSION } from "../../provisioning/default.js"

describe("CliDownloader", () => {
  describe("download()", () => {
    const osPlatform = os.platform()
    const setupSut = (options: CliDownloaderOptions) => {
      return CliDownloaderFactory.create(osPlatform, options)
    }

    describe("when valid cliVersion is provided", () => {
      it("should return downloaded bin path", async () => {
        const sut = setupSut({
          cliVersion: CLI_VERSION,
        })

        const result = await sut.download()

        assert.ok(result)
      })
    })

    describe("when invalid cliVersion is provided", () => {
      it("should throw error", async () => {
        try {
          const sut = setupSut({
            cliVersion: "invalid_version",
          })

          await sut.download()

          assert.fail("Should throw error before reaching this")
        } catch (e) {
          assert(e instanceof InitEngineSessionBinaryError)
        }
      })
    })
  })
})
