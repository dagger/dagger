---
slug: /1008/aws-cloudformation/
---

# Provision infrastructure with Dagger and AWS CloudFormation

In this guide, you will learn how to automatically [provision infrastructure](https://dzone.com/articles/infrastructure-provisioning-–) on AWS by integrating [Amazon Cloudformation](https://aws.amazon.com/cloudformation/) in your Dagger environment.

We will start with something simple: provisioning a new bucket on [Amazon S3](https://en.wikipedia.org/wiki/Amazon_S3). But Cloudformation can provision almost any AWS resource, and Dagger can integrate with the full Cloudformation API.

## Prerequisites

### Reminder

#### Guidelines

The provisioning strategy detailed below follows S3 best practices. However, to remain agnostic of your current AWS level, it profoundly relies on S3 and Cloudformation documentation.

#### Relays

The first thing to consider when developing a plan based on relays is to read their universe reference: it summarizes the expected inputs and their corresponding formats. [Here](/reference/universe/aws/cloudformation) is the Cloudformation one.

## Initialize a Dagger Workspace and Environment

### (optional) Setup example app

You will need the local copy of the [Dagger examples repository](https://github.com/dagger/examples) used in previous guides

```shell
git clone https://github.com/dagger/examples
```

Make sure to run all commands from the todoapp directory:

```shell
cd examples/todoapp
```

### (optional) Initialize a Cue module

This guide will use the same directory as the root of the Dagger workspace and the root of the Cue module, but you can create your Cue module anywhere inside the Dagger workspace.

```shell
cue mod init
```

### Organize your package

Let's create a new directory for our Cue package:

```shell
mkdir cloudformation
```

## Create a basic plan

Let's implement the Cloudformation template and convert it to a Cue definition for further flexibility.

### Setup the template and the environment

#### Setup the template

The idea here is to follow best practices in [S3 buckets](https://docs.aws.amazon.com/AmazonS3/latest/userguide/HostingWebsiteOnS3Setup.html) provisioning. Thankfully, the AWS documentation contains a working [Cloudformation template](https://docs.aws.amazon.com/fr_fr/AWSCloudFormation/latest/UserGuide/quickref-s3.html#scenario-s3-bucket-website) that fits 95% of our needs.

##### 1. Tweaking the template: output bucket name only

Create a file named `template.cue` and add the following configuration to it.

```cue title="todoapp/cloudformation/template.cue"
package cloudformation

// inlined s3 cloudformation template as a string
template: """
  {
    "AWSTemplateFormatVersion": "2010-09-09",
    "Resources": {
      "S3Bucket": {
        "Type": "AWS::S3::Bucket",
        "Properties": {
          "AccessControl": "PublicRead",
          "WebsiteConfiguration": {
            "IndexDocument": "index.html",
            "ErrorDocument": "error.html"
          }
        },
        "DeletionPolicy": "Retain"
      },
      "BucketPolicy": {
        "Type": "AWS::S3::BucketPolicy",
        "Properties": {
          "PolicyDocument": {
            "Id": "MyPolicy",
            "Version": "2012-10-17",
            "Statement": [
              {
                "Sid": "PublicReadForGetBucketObjects",
                "Effect": "Allow",
                "Principal": "*",
                "Action": "s3:GetObject",
                "Resource": {
                  "Fn::Join": [
                    "",
                    [
                      "arn:aws:s3:::",
                      {
                        "Ref": "S3Bucket"
                      },
                      "/*"
                    ]
                  ]
                }
              }
            ]
          },
          "Bucket": {
            "Ref": "S3Bucket"
          }
        }
      }
    },
    "Outputs": {
      "Name": {
        "Value": {
          "Fn::GetAtt": ["S3Bucket", "Arn"]
        },
        "Description": "Name S3 Bucket"
      }
    }
  }
"""
```

##### 2. Cloudformation relay

As our plan relies on [Cloudformation's relay](/reference/universe/aws/cloudformation), let's dissect the expected inputs by gradually incorporating them into our plan.

```shell
dagger doc alpha.dagger.io/aws/cloudformation
# Inputs:
#     config.region       string                                   AWS region
#     config.accessKey    dagger.#Secret                           AWS access key
#     config.secretKey    dagger.#Secret                           AWS secret key
#     source              string                                   Source is the Cloudformation template (JSON/YAML…
#     stackName           string                                   Stackname is the cloudformation stack
#     parameters          struct                                   Stack parameters
#     onFailure           *"DO_NOTHING" | "ROLLBACK" | "DELETE"    Behavior when failure to create/update the Stack
#     timeout             *10 | >=0 & int                          Maximum waiting time until stack creation/update…
#     neverUpdate         *false | true                            Never update the stack if already exists
```

###### 1. General insights

As seen above in the documentation, values starting with `*` are default values. However, as a plan developer, we may need to add default values to inputs from relays without one: Cue gives you this flexibility.

###### 2. The config value

The config values are all part of the `aws` relay. Regarding this package, as you can see above, five of the required inputs miss default options (`parameters` field is optional):

> - _config.region_
> - _config.accessKey_
> - _config.secretKey_
> - _source_
> - _stackName_

Let's implement the first step, use the `aws.#Config` relay, and request its first inputs: the region to deploy and the AWS credentials.

```cue title="todoapp/cloudformation/source.cue"
package cloudformation

import (
  "alpha.dagger.io/aws"
)

// AWS account: credentials and region
awsConfig: aws.#Config
```

This defines:

- `awsConfig`: AWS CLI Configuration step using the package `alpha.dagger.io/aws`. It takes three user inputs: a `region`, an `accessKey`, and a `secretKey`

#### Setup the environment

##### 1. Create a new environment

Now that the Cue package is ready, let's create an environment to run it:

```shell
dagger new 'cloudformation' -p ./cloudformation
```

##### 2. Check plan

_Pro tips_: To check whether it worked or not, these three commands might help

```shell
dagger input list -e cloudformation # List our personal plan's inputs
# Input                Value                  Set by user  Description
# awsConfig.region     string                 false        AWS region
# awsConfig.accessKey  dagger.#Secret         false        AWS access key
# awsConfig.secretKey  dagger.#Secret         false        AWS secret key

dagger query -e cloudformation # Query values / inspect default values (Instrumental in case of conflict)
# {}

dagger up -e cloudformation # Try to run the plan. As expected, we encounter a failure because some user inputs haven't been set
# 4:11PM ERR system | required input is missing    input=awsConfig.region
# 4:11PM ERR system | required input is missing    input=awsConfig.accessKey
# 4:11PM ERR system | required input is missing    input=awsConfig.secretKey
# 4:11PM FTL system | some required inputs are not set, please re-run with `--force` if you think it's a mistake    missing=0s
```

#### Finish template setup

Now that we have the `config` definition properly configured, let's modify the Cloudformation one:

```cue title="todoapp/cloudformation/source.cue"
package cloudformation

import (
  "alpha.dagger.io/aws"
  "alpha.dagger.io/dagger"
  "alpha.dagger.io/random"
  "alpha.dagger.io/aws/cloudformation"
)

// AWS account: credentials and region
awsConfig: aws.#Config

// Create a random suffix
suffix: random.#String & {
  seed: ""
}

// Query the Cloudformation stackname, or create one with a random suffix to keep unicity
cfnStackName: *"stack-\(suffix.out)" | string & dagger.#Input

// AWS Cloudformation stdlib
cfnStack: cloudformation.#Stack & {
  config:    awsConfig
  stackName: cfnStackName
  source:    template
}
```

This defines:

- `suffix`: random suffix leveraging the `random` relay. It doesn't have a seed because we don't care about predictability
- `cfnStackName`: Name of the stack, either a default value `stack-suffix` or user input
- `cfnStack`: Cloudformation relay with `AWS config`, `stackName` and `JSON template` as inputs

### Configure the environment

Before bringing up the deployment, we need to provide the `cfnStack` inputs declared in the configuration. Otherwise, Dagger will complain about missing inputs.

```shell
dagger up -e cloudformation
# 3:34PM ERR system | required input is missing    input=awsConfig.region
# 3:34PM ERR system | required input is missing    input=awsConfig.accessKey
# 3:34PM ERR system | required input is missing    input=awsConfig.secretKey
# 3:34PM FTL system | some required inputs are not set, please re-run with `--force` if you think it's a mistake    missing=0s
```

You can inspect the list of inputs (both required and optional) using dagger input list:

```shell
dagger input list -e cloudformation
# Input                 Value                                  Set by user  Description
# awsConfig.region      string                                 false        AWS region
# awsConfig.accessKey   dagger.#Secret                         false        AWS access key
# awsConfig.secretKey   dagger.#Secret                         false        AWS secret key
# suffix.length         *12 | number                           false        length of the string
# cfnStack.onFailure    *"DO_NOTHING" | "ROLLBACK" | "DELETE"  false        Behavior when failure to create/update the Stack
# cfnStack.timeout      *10 | >=0 & int                        false        Maximum waiting time until stack creation/update (in minutes)
# cfnStack.neverUpdate  *false | true                          false        Never update the stack if already exists
```

Let's provide the missing inputs:

```shell
dagger input text awsConfig.region us-east-2 -e cloudformation
dagger input secret awsConfig.accessKey yourAccessKey -e cloudformation
dagger input secret awsConfig.secretKey yourSecretKey -e cloudformation
```

### Deploying

Finally ! We now have a working template ready to be used to provision S3 infrastructures. Let's deploy it:
<Tabs
  defaultValue="nd"
  values={[
    { label: 'Normal deploy', value: 'nd', },
    { label: 'Debug deploy', value: 'dd', },
  ]
}>
<TabItem value="nd">

```shell
dagger up -e cloudformation
#2:22PM INF suffix.out | computing
#2:22PM INF suffix.out | completed    duration=200ms
#2:22PM INF cfnStack.outputs | computing
#2:22PM INF cfnStack.outputs | #15 1.304 {
#2:22PM INF cfnStack.outputs | #15 1.304     "Parameters": []
#2:22PM INF cfnStack.outputs | #15 1.304 }
#2:22PM INF cfnStack.outputs | #15 2.948 {
#2:22PM INF cfnStack.outputs | #15 2.948     "StackId": "arn:aws:cloudformation:us-east-2:817126022176:stack/stack-emktqcfwksng/207d29a0-cd0b-11eb-aafd-0a6bae5481b4"
#2:22PM INF cfnStack.outputs | #15 2.948 }
#2:22PM INF cfnStack.outputs | completed    duration=35s

dagger output list -e cloudformation
# Output                 Value                                                    Description
# suffix.out             "emktqcfwksng"                                           generated random string
# cfnStack.outputs.Name  "arn:aws:s3:::stack-emktqcfwksng-s3bucket-9eiowjs1jab4"  -
```

</TabItem>
<TabItem value="dd">

```shell
dagger up -l debug -e cloudformation
#Output:
# 3:50PM DBG system | detected buildkit version    version=v0.8.3
# 3:50PM DBG system | spawning buildkit job    localdirs={
#     "/tmp/infra-provisioning/.dagger/env/infra/plan": "/tmp/infra-provisioning/.dagger/env/infra/plan"
# } attrs=null
# 3:50PM DBG system | loading configuration
# ... Lots of logs ... :-D
# Output                 Value                                                    Description
# suffix.out             "abnyiemsoqbm"                                           generated random string
# cfnStack.outputs.Name  "arn:aws:s3:::stack-abnyiemsoqbm-s3bucket-9eiowjs1jab4"  -

dagger output list -e cloudformation
# Output                 Value                                                    Description
# suffix.out             "abnyiemsoqbm"                                           generated random string
# cfnStack.outputs.Name  "arn:aws:s3:::stack-abnyiemsoqbm-s3bucket-9eiowjs1jab4"  -
```

</TabItem>
</Tabs>

The deployment went well!

In case of a failure, the `Debug deploy` tab shows the command to get more information.
The name of the provisioned S3 instance lies in the `cfnStack.outputs.Name` output key, without `arn:aws:s3:::`

> With this provisioning infrastructure, your dev team will easily be able to instantiate aws infrastructures: all they need to know is `dagger input list -e cloudformation` and `dagger up -e cloudformation` isn't that awesome? :-D

## Cue Cloudformation template

This section will convert the inlined JSON template to CUE to take advantage of the language features.

To do so quickly, we will first transform the template from JSON format to Cue format, then optimize it to leverage Cue's forces.

### 1. Create convert.cue

We will create a new `convert.cue` file to process the conversion

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

<Tabs
  defaultValue="sv"
  values={[
    { label: 'JSON Generic Code', value: 'sv', },
    { label: 'YAML Generic Code', value: 'yv', },
  ]
}>
<TabItem value="sv">

```cue title="todoapp/cloudformation/convert.cue"
package cloudformation
import "encoding/json"

s3Template: json.Unmarshal(template)
```

</TabItem>
<TabItem value="yv">

```cue title="todoapp/cloudformation/convert.cue"
package cloudformation
import "encoding/yaml"

s3Template: yaml.Unmarshal(template)
```

</TabItem>
</Tabs>

This defines:

- `s3Template`: contains the unmarshalled template.

You need to empty the plan and copy the `convert.cue` file to the plan for Dagger to reference it

```shell
mv cloudformation/source.cue ~/tmp/
```

### 2. Retrieve the Unmarshalled JSON

Then, still in the same folder, query the `s3Template` value to retrieve the Unmarshalled result of `s3`:

```shell
dagger query s3Template -e cloudformation
# {
#   "AWSTemplateFormatVersion": "2010-09-09",
#   "Outputs": {
#     "Name": {
#       "Description": "Name S3 Bucket",
#       "Value": {
#         "Fn::GetAtt": [
#           "S3Bucket",
#           "Arn"
#           ...
```

The commented output above is the cue version of the JSON Template, copy it

### 3. Remove convert.cue

```shell
rm cloudformation/convert.cue
```

### 4. Store the output

Open `cloudformation/template.cue` and append below elements with copied Cue definition of the JSON:

```cue title="todoapp/cloudformation/template.cue"
// Add this line, to make it part to the cloudformation template
package cloudformation
import "encoding/json"

// Wrap exported Cue in previous point inside the `s3` value
s3: {
  "AWSTemplateFormatVersion": "2010-09-09",
  "Outputs": {
    "Name": {
      "Description": "Name S3 Bucket",
      "Value": {
        "Fn::GetAtt": [
          "S3Bucket",
          "Arn"
        ]
      }
    }
  },
  "Resources": {
    "BucketPolicy": {
      "Properties": {
        "Bucket": {
          "Ref": "S3Bucket"
        },
        "PolicyDocument": {
          "Id": "MyPolicy",
          "Statement": [
            {
              "Action": "s3:GetObject",
              "Effect": "Allow",
              "Principal": "*",
              "Resource": {
                "Fn::Join": [
                  "",
                  [
                    "arn:aws:s3:::",
                    {
                      "Ref": "S3Bucket"
                    },
                    "/*"
                  ]
                ]
              },
              "Sid": "PublicReadForGetBucketObjects"
            }
          ],
          "Version": "2012-10-17"
        }
      },
      "Type": "AWS::S3::BucketPolicy"
    },
    "S3Bucket": {
      "DeletionPolicy": "Retain",
      "Properties": {
        "AccessControl": "PublicRead",
        "WebsiteConfiguration": {
          "ErrorDocument": "error.html",
          "IndexDocument": "index.html"
        }
      },
      "Type": "AWS::S3::Bucket"
    }
  }
}

// Template contains the marshalled value of the s3 template
template: json.Marshal(s3)
```

We're using the built-in `json.Marshal` function to convert CUE back to JSON, so Cloudformation still receives the same template.

You can inspect the configuration using `dagger query -e cloudformation` to verify it produces the same manifest:

```shell
dagger query template -f text -e cloudformation
```

Now that the template is defined in CUE, we can use the language to add more flexibility to our template.

Let's define a re-usable `#Deployment` definition in `todoapp/cloudformation/deployment.cue`:

```cue title="todoapp/cloudformation/deployment.cue"
package cloudformation

#Deployment: {

  // Bucket's output description
  description: string

  // index file
  indexDocument: *"index.html" | string

  // error file
  errorDocument: *"error.html" | string

  // Bucket policy version
  version: *"2012-10-17" | string

  // Retain as default deletion policy. Delete is also accepted but requires the s3 bucket to be empty
  deletionPolicy: *"Retain" | "Delete"

  // Canned access control list (ACL) that grants predefined permissions to the bucket
  accessControl: *"PublicRead" | "Private" | "PublicReadWrite" | "AuthenticatedRead" | "LogDeliveryWrite" | "BucketOwnerRead" | "BucketOwnerFullControl" | "AwsExecRead"

  // Modified copy of s3 value in `todoapp/cloudformation/template.cue`
  template: {
    "AWSTemplateFormatVersion": "2010-09-09",
    "Outputs": {
      "Name": {
        "Description": description,
        "Value": {
          "Fn::GetAtt": [
            "S3Bucket",
            "Arn"
          ]
        }
      }
    },
    "Resources": {
      "BucketPolicy": {
        "Properties": {
          "Bucket": {
            "Ref": "S3Bucket"
          },
          "PolicyDocument": {
            "Id": "MyPolicy",
            "Statement": [
              {
                "Action": "s3:GetObject",
                "Effect": "Allow",
                "Principal": "*",
                "Resource": {
                  "Fn::Join": [
                    "",
                    [
                      "arn:aws:s3:::",
                      {
                        "Ref": "S3Bucket"
                      },
                      "/*"
                    ]
                  ]
                },
                "Sid": "PublicReadForGetBucketObjects"
              }
            ],
            "Version": version
          }
        },
        "Type": "AWS::S3::BucketPolicy"
      },
      "S3Bucket": {
        "DeletionPolicy": deletionPolicy,
        "Properties": {
          "AccessControl": "PublicRead",
          "WebsiteConfiguration": {
            "ErrorDocument": errorDocument,
            "IndexDocument": indexDocument
          }
        },
        "Type": "AWS::S3::Bucket"
      }
    }
  }
}
```

`template.cue` can be rewritten as follows:

```cue title="todoapp/cloudformation/template.cue"
package cloudformation
import "encoding/json"

s3: #Deployment & {
  description: "Name S3 Bucket"
}

// Template contains the marshalled value of the s3 template
template: json.Marshal(s3.template)
```

Verify template

Double-checks at the template level can be done with manual uploads on Cloudformation's web interface or by executing the below command locally:

```shell
tmpfile=$(mktemp ./tmp.XXXXXX) && dagger query template -f text -e cloudformation > "$tmpfile" && aws cloudformation validate-template  --template-body file://"$tmpfile" ; rm "$tmpfile"
```

Let's make sure it yields the same result:

```shell
dagger query template -f text -e cloudformation
# {
#   "description": "Name S3 Bucket",
#   "indexDocument": "index.html",
#   "errorDocument": "error.html",
#   "version": "2012-10-17",
#   "deletionPolicy": "Retain",
#   "accessControl": "PublicRead",
#   "template": {
#     "AWSTemplateFormatVersion": "2010-09-09",
#     "Outputs": {
#       "Name": {
#         "Description": "Name S3 Bucket",
#         "Value": {
```

You need to move back the `source.cue` for Dagger to instanciate a bucket:

```shell
mv ~/tmp/source.cue cloudformation/source.cue
```

And we can now deploy it:

```shell
dagger up -e cloudformation
#2:22PM INF suffix.out | computing
#2:22PM INF suffix.out | completed    duration=200ms
#2:22PM INF cfnStack.outputs | computing
#2:22PM INF cfnStack.outputs | #15 1.304 {
#2:22PM INF cfnStack.outputs | #15 1.304     "Parameters": []
#2:22PM INF cfnStack.outputs | #15 1.304 }
#2:22PM INF cfnStack.outputs | #15 2.948 {
#2:22PM INF cfnStack.outputs | #15 2.948     "StackId": "arn:aws:cloudformation:us-east-2:817126022176:stack/stack-emktqcfwksng/207d29a0-cd0b-11eb-aafd-0a6bae5481b4"
#2:22PM INF cfnStack.outputs | #15 2.948 }
#2:22PM INF cfnStack.outputs | completed    duration=35s
```

Name of the deployed bucket:

```shell
dagger output list -e cloudformation
# Output                 Value                                                    Description
# suffix.out             "ucwcecwwshdl"                                           generated random string
# cfnStack.outputs.Name  "arn:aws:s3:::stack-ucwcecwwshdl-s3bucket-gaqmj8rzsl08"  -
```

The name of the provisioned S3 instance lies in the `cfnStack.outputs.Name` output key, without `arn:aws:s3:::`

PS: This plan could be further extended with the AWS S3 example. It could provide infrastructure and quickly deploy it.

PS1: As it could be an excellent first exercise for you, this won't be detailed here. However, we're interested in your imagination: let us know your implementations :-)
