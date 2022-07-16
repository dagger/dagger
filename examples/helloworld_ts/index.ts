import Dagger from "dagger";

const dagger = new Dagger();

dagger.action("uppercase", ({ message }) => ({
  response: message.toUpperCase(),
}));

dagger.action("echo", async ({ message }) => {
  await dagger.do(`mutation{import(ref:"alpine"){name}}`);
  const output = await dagger.do(`{alpine{build(pkgs:["jq"]){fs}}}`);
  return {
    fs: output.data.data.alpine.build.fs,
  };
});
