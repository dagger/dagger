import * as random from "@pulumi/random";

export const randomInt = new random.RandomInteger("randomInt", {
  min: 1,
  max: 10,
});
