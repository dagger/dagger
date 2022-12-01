import assert from "assert"

import { DEFAULT_HOST } from "../default.js"
import { getProvisioner } from "../provisioner.js"

describe("Provisioner", function () {
  describe("Docker Image", function () {
    it.skip("Should create a GQL client on call to connect", async function () {
      this.timeout(30000)
      const provisioner = getProvisioner(DEFAULT_HOST)

      after(async function () {
        await provisioner.Close()
      })

      const client = await provisioner.Connect({})
      assert.notEqual(client, undefined)
    })
  })
})
