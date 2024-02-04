import { connect } from "../typescript/dist";
import { expect, test, describe } from "bun:test";

describe("scaffolding bun test", () => {
  test("hello world", async () => {
    await connect(async (client) => {
      const out = await client
        .container()
        .from("alpine:3.16.2")
        .withExec(["echo", "hello", "world!"])
        .stdout();

      expect(out).toBe("hello world!\n");
    });
  });
});
