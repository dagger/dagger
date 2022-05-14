# GCP Examples

You can have a look at the [plan.cue](./plan.cue) file which contains the example to deploy a serverless cloud function with GCP.

If you want to deploy this cloud function, you can simply provide your service key and your project name.

The project name needs to be provided through the GCP\_PROJECT environment variable.

The service key needs to be in a `secrets` folder and be named `serviceKey.json`

Once you've setup everything, you can simply run:

```shell
dagger do HelloWorld
```
It will deploy a cloud function sending back HelloWorld when you make a request on the url.
