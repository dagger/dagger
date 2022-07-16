import Dagger from "dagger";

const dagger = new Dagger();

dagger.action("uppercase", ({ message }) => ({ response: message.toUpperCase() }))

dagger.action("echo", async ({ message }) => {
    // const output = await dagger.do(`{alpine{build(packages: ["jq"])}}`);
    const output = await dagger.do(`{core{image(ref:"alpine:3.15"){fs}}}`)
    console.log("OUTPUT", output);
    console.log("DATA: ", output.data);
    console.log("CORE: ", output.data.core);
    console.log("FS: ", output.data.core.image.fs)
    return {
        "fs": output.data.core.image.fs,
    };
})
