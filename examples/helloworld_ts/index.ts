import Dagger from "dagger";

const dagger = new Dagger();

dagger.action("uppercase", ({ message }) => ({
  response: message.toUpperCase(),
}));

dagger.action("echo", async ({ message }) => {
  return message;
});

dagger.action("build", async ({ pkg }) => {
  await dagger.do(`mutation{
    import(ref:"alpine") {
      name
    }
  }`);

  const input = `{
    alpine {
      build(pkgs:[${JSON.stringify(pkg)}])
    }
  }`;
  console.log("input: ", input);

  const output = await dagger.do(input);
  return {
    fs: output.data.data.alpine.build,
    test: pkg,
  };
});
