namespace Dagger.SDK.SourceGenerator.Tests;

public static class TestData
{
    public const string Schema = """
        {
          "__schema": {
            "directives": [
              {
                "args": [
                  {
                    "defaultValue": "\"No longer supported\"",
                    "description": "Explains why this element was deprecated, usually also including a suggestion for how to access supported similar data. Formatted in [Markdown](https://daringfireball.net/projects/markdown/).",
                    "name": "reason",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "description": "The @deprecated built-in directive is used within the type system definition language to indicate deprecated portions of a GraphQL service's schema, such as deprecated fields on a type, arguments on a field, input fields on an input type, or values of an enum type.",
                "locations": [
                  "FIELD_DEFINITION",
                  "ARGUMENT_DEFINITION",
                  "INPUT_FIELD_DEFINITION",
                  "ENUM_VALUE"
                ],
                "name": "deprecated"
              },
              {
                "args": [
                  {
                    "defaultValue": null,
                    "description": "Explains why this element is impure, i.e. whether it performs side effects or yield a different result with the same arguments.",
                    "name": "reason",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "description": "Indicates that a field may resolve to different values when called repeatedly with the same inputs, or that the field has side effects. Impure fields are never cached.",
                "locations": ["FIELD_DEFINITION"],
                "name": "impure"
              },
              {
                "args": [],
                "description": "Indicates that a field's selection can be removed from any query without changing the result. Meta fields are dropped from cache keys.",
                "locations": ["FIELD_DEFINITION"],
                "name": "meta"
              }
            ],
            "mutationType": null,
            "queryType": {
              "name": "Query"
            },
            "subscriptionType": null,
            "types": [
              {
                "description": "The `Boolean` scalar type represents `true` or `false`.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "Boolean",
                "possibleTypes": []
              },
              {
                "description": "Key value object that represents a build argument.",
                "enumValues": [],
                "fields": [],
                "inputFields": [
                  {
                    "defaultValue": null,
                    "description": "The build argument name.",
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "defaultValue": null,
                    "description": "The build argument value.",
                    "name": "value",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "interfaces": [],
                "kind": "INPUT_OBJECT",
                "name": "BuildArg",
                "possibleTypes": []
              },
              {
                "description": "Sharing mode of the cache volume.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "Shares the cache volume amongst many build pipelines",
                    "isDeprecated": false,
                    "name": "SHARED"
                  },
                  {
                    "deprecationReason": null,
                    "description": "Keeps a cache volume for a single build pipeline",
                    "isDeprecated": false,
                    "name": "PRIVATE"
                  },
                  {
                    "deprecationReason": null,
                    "description": "Shares the cache volume amongst many build pipelines, but will serialize the writes",
                    "isDeprecated": false,
                    "name": "LOCKED"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "CacheSharingMode",
                "possibleTypes": []
              },
              {
                "description": "A directory whose contents persist across runs.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this CacheVolume.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "CacheVolumeID",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "CacheVolume",
                "possibleTypes": []
              },
              {
                "description": "The `CacheVolumeID` scalar type represents an identifier for an object of type CacheVolume.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "CacheVolumeID",
                "possibleTypes": []
              },
              {
                "description": "An OCI-compatible container, also known as a Docker container.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Turn the container into a Service.\n\nBe sure to set any exposed ports before this conversion.",
                    "isDeprecated": false,
                    "name": "asService",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Service",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "[]",
                        "description": "Identifiers for other platform specific containers.\n\nUsed for multi-platform images.",
                        "name": "platformVariants",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "ContainerID",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Force each layer of the image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.",
                        "name": "forcedCompression",
                        "type": {
                          "kind": "ENUM",
                          "name": "ImageLayerCompression",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "OCIMediaTypes",
                        "description": "Use the specified media types for the image's layers.\n\nDefaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.",
                        "name": "mediaTypes",
                        "type": {
                          "kind": "ENUM",
                          "name": "ImageMediaTypes",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a File representing the container serialized to a tarball.",
                    "isDeprecated": false,
                    "name": "asTarball",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Directory context used by the Dockerfile.",
                        "name": "context",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"Dockerfile\"",
                        "description": "Path to the Dockerfile to use.",
                        "name": "dockerfile",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Target build stage to build.",
                        "name": "target",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Additional build arguments.",
                        "name": "buildArgs",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "INPUT_OBJECT",
                              "name": "BuildArg",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Secrets to pass to the build.\n\nThey will be mounted at /run/secrets/[secret-name] in the build container\n\nThey can be accessed in the Dockerfile using the \"secret\" mount type and mount path /run/secrets/[secret-name], e.g. RUN --mount=type=secret,id=my-secret curl [http://example.com?token=$(cat /run/secrets/my-secret)](http://example.com?token=$(cat /run/secrets/my-secret))",
                        "name": "secrets",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "SecretID",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Initializes this container from a Dockerfile build.",
                    "isDeprecated": false,
                    "name": "build",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves default arguments for future commands.",
                    "isDeprecated": false,
                    "name": "defaultArgs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The path of the directory to retrieve (e.g., \"./src\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves a directory at the given path.\n\nMounts are included.",
                    "isDeprecated": false,
                    "name": "directory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves entrypoint to be prepended to the arguments of all commands.",
                    "isDeprecated": false,
                    "name": "entrypoint",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the environment variable to retrieve (e.g., \"PATH\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves the value of the specified environment variable.",
                    "isDeprecated": false,
                    "name": "envVariable",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the list of environment variables passed to commands.",
                    "isDeprecated": false,
                    "name": "envVariables",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "EnvVariable",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "EXPERIMENTAL API! Subject to change/removal at any time.\n\nConfigures all available GPUs on the host to be accessible to this container.\n\nThis currently works for Nvidia devices only.",
                    "isDeprecated": false,
                    "name": "experimentalWithAllGPUs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "List of devices to be accessible to this container.",
                        "name": "devices",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "EXPERIMENTAL API! Subject to change/removal at any time.\n\nConfigures the provided list of devices to be accessible to this container.\n\nThis currently works for Nvidia devices only.",
                    "isDeprecated": false,
                    "name": "experimentalWithGPU",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Host's destination path (e.g., \"./tarball\").\n\nPath can be relative to the engine's workdir or absolute.",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Identifiers for other platform specific containers.\n\nUsed for multi-platform image.",
                        "name": "platformVariants",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "ContainerID",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Force each layer of the exported image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.",
                        "name": "forcedCompression",
                        "type": {
                          "kind": "ENUM",
                          "name": "ImageLayerCompression",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "OCIMediaTypes",
                        "description": "Use the specified media types for the exported image's layers.\n\nDefaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.",
                        "name": "mediaTypes",
                        "type": {
                          "kind": "ENUM",
                          "name": "ImageMediaTypes",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Writes the container as an OCI tarball to the destination file path on the host.\n\nReturn true on success.\n\nIt can also export platform variants.",
                    "isDeprecated": false,
                    "name": "export",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the list of exposed ports.\n\nThis includes ports already exposed by the image, even if not explicitly added with dagger.",
                    "isDeprecated": false,
                    "name": "exposedPorts",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "Port",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The path of the file to retrieve (e.g., \"./README.md\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves a file at the given path.\n\nMounts are included.",
                    "isDeprecated": false,
                    "name": "file",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Image's address from its registry.\n\nFormatted as [host]/[user]/[repo]:[tag] (e.g., \"docker.io/dagger/dagger:main\").",
                        "name": "address",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Initializes this container from a pulled base image.",
                    "isDeprecated": false,
                    "name": "from",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Container.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ContainerID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The unique image reference which can only be retrieved immediately after the 'Container.From' call.",
                    "isDeprecated": false,
                    "name": "imageRef",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "File to read the container from.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FileID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Identifies the tag to import from the archive, if the archive bundles multiple tags.",
                        "name": "tag",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Reads the container from an OCI tarball.",
                    "isDeprecated": false,
                    "name": "import",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the label (e.g., \"org.opencontainers.artifact.created\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves the value of the specified label.",
                    "isDeprecated": false,
                    "name": "label",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the list of labels passed to container.",
                    "isDeprecated": false,
                    "name": "labels",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "Label",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the list of paths where a directory is mounted.",
                    "isDeprecated": false,
                    "name": "mounts",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Name of the sub-pipeline.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Description of the sub-pipeline.",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Labels to apply to the sub-pipeline.",
                        "name": "labels",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "INPUT_OBJECT",
                              "name": "PipelineLabel",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a named sub-pipeline.",
                    "isDeprecated": false,
                    "name": "pipeline",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The platform this container executes and publishes as.",
                    "isDeprecated": false,
                    "name": "platform",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Platform",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Registry's address to publish the image to.\n\nFormatted as [host]/[user]/[repo]:[tag] (e.g. \"docker.io/dagger/dagger:main\").",
                        "name": "address",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Identifiers for other platform specific containers.\n\nUsed for multi-platform image.",
                        "name": "platformVariants",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "ContainerID",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Force each layer of the published image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.",
                        "name": "forcedCompression",
                        "type": {
                          "kind": "ENUM",
                          "name": "ImageLayerCompression",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "OCIMediaTypes",
                        "description": "Use the specified media types for the published image's layers.\n\nDefaults to OCI, which is largely compatible with most recent registries, but Docker may be needed for older registries without OCI support.",
                        "name": "mediaTypes",
                        "type": {
                          "kind": "ENUM",
                          "name": "ImageMediaTypes",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Publishes this container as a new image to the specified address.\n\nPublish returns a fully qualified ref.\n\nIt can also publish platform variants.",
                    "isDeprecated": false,
                    "name": "publish",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves this container's root filesystem. Mounts are not included.",
                    "isDeprecated": false,
                    "name": "rootfs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The error stream of the last executed command.\n\nWill execute default command if none is set, or error if there's no default.",
                    "isDeprecated": false,
                    "name": "stderr",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The output stream of the last executed command.\n\nWill execute default command if none is set, or error if there's no default.",
                    "isDeprecated": false,
                    "name": "stdout",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Forces evaluation of the pipeline in the engine.\n\nIt doesn't run the default command if no exec has been set.",
                    "isDeprecated": false,
                    "name": "sync",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ContainerID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "[]",
                        "description": "If set, override the container's default terminal command and invoke these command arguments instead.",
                        "name": "cmd",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Provides Dagger access to the executed command.\n\nDo not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.",
                        "name": "experimentalPrivilegedNesting",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.",
                        "name": "insecureRootCapabilities",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Return an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).",
                    "isDeprecated": false,
                    "name": "terminal",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Terminal",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the user to be set for all commands.",
                    "isDeprecated": false,
                    "name": "user",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Arguments to prepend to future executions (e.g., [\"-v\", \"--no-cache\"]).",
                        "name": "args",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Configures default arguments for future commands.",
                    "isDeprecated": false,
                    "name": "withDefaultArgs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The args of the command.",
                        "name": "args",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Provides Dagger access to the executed command.\n\nDo not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.",
                        "name": "experimentalPrivilegedNesting",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.",
                        "name": "insecureRootCapabilities",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Set the default command to invoke for the container's terminal API.",
                    "isDeprecated": false,
                    "name": "withDefaultTerminalCmd",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the written directory (e.g., \"/tmp/directory\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the directory to write",
                        "name": "directory",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Patterns to exclude in the written directory (e.g. [\"node_modules/**\", \".gitignore\", \".git/\"]).",
                        "name": "exclude",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Patterns to include in the written directory (e.g. [\"*.go\", \"go.mod\", \"go.sum\"]).",
                        "name": "include",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the directory and its contents.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a directory written at the given path.",
                    "isDeprecated": false,
                    "name": "withDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Entrypoint to use for future executions (e.g., [\"go\", \"run\"]).",
                        "name": "args",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Don't remove the default arguments when setting the entrypoint.",
                        "name": "keepDefaultArgs",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container but with a different command entrypoint.",
                    "isDeprecated": false,
                    "name": "withEntrypoint",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the environment variable (e.g., \"HOST\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The value of the environment variable. (e.g., \"localhost\").",
                        "name": "value",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Replace `${VAR}` or `$VAR` in the value according to the current environment variables defined in the container (e.g., \"/opt/bin:$PATH\").",
                        "name": "expand",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus the given environment variable.",
                    "isDeprecated": false,
                    "name": "withEnvVariable",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Command to run instead of the container's default command (e.g., [\"run\", \"main.go\"]).\n\nIf empty, the container's default command is used.",
                        "name": "args",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "If the container has an entrypoint, ignore it for args rather than using it to wrap them.",
                        "name": "skipEntrypoint",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Content to write to the command's standard input before closing (e.g., \"Hello world\").",
                        "name": "stdin",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Redirect the command's standard output to a file in the container (e.g., \"/tmp/stdout\").",
                        "name": "redirectStdout",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Redirect the command's standard error to a file in the container (e.g., \"/tmp/stderr\").",
                        "name": "redirectStderr",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Provides Dagger access to the executed command.\n\nDo not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.",
                        "name": "experimentalPrivilegedNesting",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.",
                        "name": "insecureRootCapabilities",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container after executing the specified command inside it.",
                    "isDeprecated": false,
                    "name": "withExec",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Port number to expose",
                        "name": "port",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "Int",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "TCP",
                        "description": "Transport layer network protocol",
                        "name": "protocol",
                        "type": {
                          "kind": "ENUM",
                          "name": "NetworkProtocol",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Optional port description",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Skip the health check when run as a service.",
                        "name": "experimentalSkipHealthcheck",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Expose a network port.\n\nExposed ports serve two purposes:\n\n- For health checks and introspection, when running services\n\n- For setting the EXPOSE OCI field when publishing the container",
                    "isDeprecated": false,
                    "name": "withExposedPort",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the copied file (e.g., \"/tmp/file.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the file to copy.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FileID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Permission given to the copied file (e.g., 0600).",
                        "name": "permissions",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the file.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus the contents of the given file copied to the given path.",
                    "isDeprecated": false,
                    "name": "withFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location where copied files should be placed (e.g., \"/src\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifiers of the files to copy.",
                        "name": "sources",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "FileID",
                                "ofType": null
                              }
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Permission given to the copied files (e.g., 0600).",
                        "name": "permissions",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the files.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus the contents of the given files copied to the given path.",
                    "isDeprecated": false,
                    "name": "withFiles",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Indicate that subsequent operations should be featured more prominently in the UI.",
                    "isDeprecated": false,
                    "name": "withFocus",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the label (e.g., \"org.opencontainers.artifact.created\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The value of the label (e.g., \"2023-01-01T00:00:00Z\").",
                        "name": "value",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus the given label.",
                    "isDeprecated": false,
                    "name": "withLabel",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the cache directory (e.g., \"/cache/node_modules\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the cache volume to mount.",
                        "name": "cache",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "CacheVolumeID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the directory to use as the cache volume's root.",
                        "name": "source",
                        "type": {
                          "kind": "SCALAR",
                          "name": "DirectoryID",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "SHARED",
                        "description": "Sharing mode of the cache volume.",
                        "name": "sharing",
                        "type": {
                          "kind": "ENUM",
                          "name": "CacheSharingMode",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the mounted cache directory.\n\nNote that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a cache volume mounted at the given path.",
                    "isDeprecated": false,
                    "name": "withMountedCache",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the mounted directory (e.g., \"/mnt/directory\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the mounted directory.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the mounted directory and its contents.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a directory mounted at the given path.",
                    "isDeprecated": false,
                    "name": "withMountedDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the mounted file (e.g., \"/tmp/file.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the mounted file.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FileID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user or user:group to set for the mounted file.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a file mounted at the given path.",
                    "isDeprecated": false,
                    "name": "withMountedFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the secret file (e.g., \"/tmp/secret.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the secret to mount.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SecretID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the mounted secret.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "256",
                        "description": "Permission given to the mounted secret (e.g., 0600).\n\nThis option requires an owner to be set to be active.",
                        "name": "mode",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a secret mounted into a file at the given path.",
                    "isDeprecated": false,
                    "name": "withMountedSecret",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the temporary directory (e.g., \"/tmp/temp_dir\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.",
                    "isDeprecated": false,
                    "name": "withMountedTemp",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the written file (e.g., \"/tmp/file.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Content of the file to write (e.g., \"Hello world!\").",
                        "name": "contents",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "420",
                        "description": "Permission given to the written file (e.g., 0600).",
                        "name": "permissions",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the file.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a new file written at the given path.",
                    "isDeprecated": false,
                    "name": "withNewFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Registry's address to bind the authentication to.\n\nFormatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).",
                        "name": "address",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The username of the registry's account (e.g., \"Dagger\").",
                        "name": "username",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The API key, password or token to authenticate to this registry.",
                        "name": "secret",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SecretID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container with a registry authentication for a given address.",
                    "isDeprecated": false,
                    "name": "withRegistryAuth",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Directory to mount.",
                        "name": "directory",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves the container with the given directory mounted to /.",
                    "isDeprecated": false,
                    "name": "withRootfs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the secret variable (e.g., \"API_SECRET\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The identifier of the secret value.",
                        "name": "secret",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SecretID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus an env variable containing the given secret.",
                    "isDeprecated": false,
                    "name": "withSecretVariable",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "A name that can be used to reach the service from the container",
                        "name": "alias",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the service container",
                        "name": "service",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ServiceID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Establish a runtime dependency on a service.\n\nThe service will be started automatically when needed and detached when it is no longer needed, executing the default command if none is set.\n\nThe service will be reachable from the container via the provided hostname alias.\n\nThe service dependency will also convey to any files or directories produced by the container.",
                    "isDeprecated": false,
                    "name": "withServiceBinding",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the forwarded Unix socket (e.g., \"/tmp/socket\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the socket to forward.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SocketID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A user:group to set for the mounted socket.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.",
                        "name": "owner",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container plus a socket forwarded to the given Unix socket path.",
                    "isDeprecated": false,
                    "name": "withUnixSocket",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The user to set (e.g., \"root\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container with a different command user.",
                    "isDeprecated": false,
                    "name": "withUser",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The path to set as the working directory (e.g., \"/app\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container with a different working directory.",
                    "isDeprecated": false,
                    "name": "withWorkdir",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves this container with unset default arguments for future commands.",
                    "isDeprecated": false,
                    "name": "withoutDefaultArgs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the directory to remove (e.g., \".github/\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container with the directory at the given path removed.",
                    "isDeprecated": false,
                    "name": "withoutDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "false",
                        "description": "Don't remove the default arguments when unsetting the entrypoint.",
                        "name": "keepDefaultArgs",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container with an unset command entrypoint.",
                    "isDeprecated": false,
                    "name": "withoutEntrypoint",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the environment variable (e.g., \"HOST\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container minus the given environment variable.",
                    "isDeprecated": false,
                    "name": "withoutEnvVariable",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Port number to unexpose",
                        "name": "port",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "Int",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "TCP",
                        "description": "Port protocol to unexpose",
                        "name": "protocol",
                        "type": {
                          "kind": "ENUM",
                          "name": "NetworkProtocol",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Unexpose a previously exposed port.",
                    "isDeprecated": false,
                    "name": "withoutExposedPort",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the file to remove (e.g., \"/file.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container with the file at the given path removed.",
                    "isDeprecated": false,
                    "name": "withoutFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Indicate that subsequent operations should not be featured more prominently in the UI.\n\nThis is the initial state of all containers.",
                    "isDeprecated": false,
                    "name": "withoutFocus",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the label to remove (e.g., \"org.opencontainers.artifact.created\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container minus the given environment label.",
                    "isDeprecated": false,
                    "name": "withoutLabel",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the cache directory (e.g., \"/cache/node_modules\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container after unmounting everything at the given path.",
                    "isDeprecated": false,
                    "name": "withoutMount",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Registry's address to remove the authentication from.\n\nFormatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).",
                        "name": "address",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container without the registry authentication of a given address.",
                    "isDeprecated": false,
                    "name": "withoutRegistryAuth",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the environment variable (e.g., \"HOST\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container minus the given environment variable containing the secret.",
                    "isDeprecated": false,
                    "name": "withoutSecretVariable",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the socket to remove (e.g., \"/tmp/socket\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this container with a previously added Unix socket removed.",
                    "isDeprecated": false,
                    "name": "withoutUnixSocket",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves this container with an unset command user.\n\nShould default to root.",
                    "isDeprecated": false,
                    "name": "withoutUser",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves this container with an unset working directory.\n\nShould default to \"/\".",
                    "isDeprecated": false,
                    "name": "withoutWorkdir",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the working directory for all commands.",
                    "isDeprecated": false,
                    "name": "workdir",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Container",
                "possibleTypes": []
              },
              {
                "description": "The `ContainerID` scalar type represents an identifier for an object of type Container.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ContainerID",
                "possibleTypes": []
              },
              {
                "description": "Reflective module API provided to functions at runtime.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this CurrentModule.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "CurrentModuleID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the module being executed in",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).",
                    "isDeprecated": false,
                    "name": "source",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the directory to access (e.g., \".\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Exclude artifacts that match the given pattern (e.g., [\"node_modules/\", \".git*\"]).",
                        "name": "exclude",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Include only artifacts that match the given pattern (e.g., [\"app/\", \"package.*\"]).",
                        "name": "include",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.",
                    "isDeprecated": false,
                    "name": "workdir",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the file to retrieve (e.g., \"README.md\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.",
                    "isDeprecated": false,
                    "name": "workdirFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "CurrentModule",
                "possibleTypes": []
              },
              {
                "description": "The `CurrentModuleID` scalar type represents an identifier for an object of type CurrentModule.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "CurrentModuleID",
                "possibleTypes": []
              },
              {
                "description": "A directory.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [
                      {
                        "defaultValue": "\".\"",
                        "description": "An optional subpath of the directory which contains the module's configuration file.\n\nThis is needed when the module code is in a subdirectory but requires parent directories to be loaded in order to execute. For example, the module source code may need a go.mod, project.toml, package.json, etc. file from a parent directory.\n\nIf not set, the module source code is loaded from the root of the directory.",
                        "name": "sourceRootPath",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load the directory as a Dagger module",
                    "isDeprecated": false,
                    "name": "asModule",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Identifier of the directory to compare.",
                        "name": "other",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Gets the difference between this directory and an another directory.",
                    "isDeprecated": false,
                    "name": "diff",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the directory to retrieve (e.g., \"/src\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves a directory at the given path.",
                    "isDeprecated": false,
                    "name": "directory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The platform to build.",
                        "name": "platform",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Platform",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"Dockerfile\"",
                        "description": "Path to the Dockerfile to use (e.g., \"frontend.Dockerfile\").",
                        "name": "dockerfile",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Target build stage to build.",
                        "name": "target",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Build arguments to use in the build.",
                        "name": "buildArgs",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "INPUT_OBJECT",
                              "name": "BuildArg",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Secrets to pass to the build.\n\nThey will be mounted at /run/secrets/[secret-name].",
                        "name": "secrets",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "SecretID",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Builds a new Docker container from this directory.",
                    "isDeprecated": false,
                    "name": "dockerBuild",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the directory to look at (e.g., \"/src\").",
                        "name": "path",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a list of files and directories at the given path.",
                    "isDeprecated": false,
                    "name": "entries",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the copied directory (e.g., \"logs/\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.",
                        "name": "wipe",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Writes the contents of the directory to a path on the host.",
                    "isDeprecated": false,
                    "name": "export",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the file to retrieve (e.g., \"README.md\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves a file at the given path.",
                    "isDeprecated": false,
                    "name": "file",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Pattern to match (e.g., \"*.md\").",
                        "name": "pattern",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a list of files and directories that matche the given pattern.",
                    "isDeprecated": false,
                    "name": "glob",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Directory.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "DirectoryID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Name of the sub-pipeline.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Description of the sub-pipeline.",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Labels to apply to the sub-pipeline.",
                        "name": "labels",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "INPUT_OBJECT",
                              "name": "PipelineLabel",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a named sub-pipeline.",
                    "isDeprecated": false,
                    "name": "pipeline",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Force evaluation in the engine.",
                    "isDeprecated": false,
                    "name": "sync",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "DirectoryID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the written directory (e.g., \"/src/\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the directory to copy.",
                        "name": "directory",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Exclude artifacts that match the given pattern (e.g., [\"node_modules/\", \".git*\"]).",
                        "name": "exclude",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Include only artifacts that match the given pattern (e.g., [\"app/\", \"package.*\"]).",
                        "name": "include",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory plus a directory written at the given path.",
                    "isDeprecated": false,
                    "name": "withDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the copied file (e.g., \"/file.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifier of the file to copy.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FileID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Permission given to the copied file (e.g., 0600).",
                        "name": "permissions",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory plus the contents of the given file copied to the given path.",
                    "isDeprecated": false,
                    "name": "withFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location where copied files should be placed (e.g., \"/src\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Identifiers of the files to copy.",
                        "name": "sources",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "FileID",
                                "ofType": null
                              }
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Permission given to the copied files (e.g., 0600).",
                        "name": "permissions",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory plus the contents of the given files copied to the given path.",
                    "isDeprecated": false,
                    "name": "withFiles",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the directory created (e.g., \"/logs\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "420",
                        "description": "Permission granted to the created directory (e.g., 0777).",
                        "name": "permissions",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory plus a new directory created at the given path.",
                    "isDeprecated": false,
                    "name": "withNewDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the written file (e.g., \"/file.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Content of the written file (e.g., \"Hello world!\").",
                        "name": "contents",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "420",
                        "description": "Permission given to the copied file (e.g., 0600).",
                        "name": "permissions",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory plus a new file written at the given path.",
                    "isDeprecated": false,
                    "name": "withNewFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Timestamp to set dir/files in.\n\nFormatted in seconds following Unix epoch (e.g., 1672531199).",
                        "name": "timestamp",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "Int",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory with all file/dir timestamps set to the given time.",
                    "isDeprecated": false,
                    "name": "withTimestamps",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the directory to remove (e.g., \".github/\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory with the directory at the given path removed.",
                    "isDeprecated": false,
                    "name": "withoutDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the file to remove (e.g., \"/file.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this directory with the file at the given path removed.",
                    "isDeprecated": false,
                    "name": "withoutFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Directory",
                "possibleTypes": []
              },
              {
                "description": "The `DirectoryID` scalar type represents an identifier for an object of type Directory.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "DirectoryID",
                "possibleTypes": []
              },
              {
                "description": "An environment variable name and value.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this EnvVariable.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "EnvVariableID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The environment variable name.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The environment variable value.",
                    "isDeprecated": false,
                    "name": "value",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "EnvVariable",
                "possibleTypes": []
              },
              {
                "description": "The `EnvVariableID` scalar type represents an identifier for an object of type EnvVariable.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "EnvVariableID",
                "possibleTypes": []
              },
              {
                "description": "A definition of a field on a custom object defined in a Module.\n\nA field on an object has a static value, as opposed to a function on an object whose value is computed by invoking code (and can accept arguments).",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A doc string for the field, if any.",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this FieldTypeDef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "FieldTypeDefID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the field in lowerCamelCase format.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The type of the field.",
                    "isDeprecated": false,
                    "name": "typeDef",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "FieldTypeDef",
                "possibleTypes": []
              },
              {
                "description": "The `FieldTypeDefID` scalar type represents an identifier for an object of type FieldTypeDef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "FieldTypeDefID",
                "possibleTypes": []
              },
              {
                "description": "A file.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the contents of the file.",
                    "isDeprecated": false,
                    "name": "contents",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the written directory (e.g., \"output.txt\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "If allowParentDirPath is true, the path argument can be a directory path, in which case the file will be created in that directory.",
                        "name": "allowParentDirPath",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Writes the file to a file path on the host.",
                    "isDeprecated": false,
                    "name": "export",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this File.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "FileID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the name of the file.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the size of the file, in bytes.",
                    "isDeprecated": false,
                    "name": "size",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Int",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Force evaluation in the engine.",
                    "isDeprecated": false,
                    "name": "sync",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "FileID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Name to set file to.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this file with its name set to the given name.",
                    "isDeprecated": false,
                    "name": "withName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Timestamp to set dir/files in.\n\nFormatted in seconds following Unix epoch (e.g., 1672531199).",
                        "name": "timestamp",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "Int",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves this file with its created/modified timestamps set to the given time.",
                    "isDeprecated": false,
                    "name": "withTimestamps",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "File",
                "possibleTypes": []
              },
              {
                "description": "The `FileID` scalar type represents an identifier for an object of type File.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "FileID",
                "possibleTypes": []
              },
              {
                "description": "The `Float` scalar type represents signed double-precision fractional values as specified by [IEEE 754](http://en.wikipedia.org/wiki/IEEE_floating_point).",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "Float",
                "possibleTypes": []
              },
              {
                "description": "Function represents a resolver provided by a Module.\n\nA function always evaluates against a parent object and is given a set of named arguments.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Arguments accepted by the function, if any.",
                    "isDeprecated": false,
                    "name": "args",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "FunctionArg",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A doc string for the function, if any.",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Function.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "FunctionID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the function.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The type returned by the function.",
                    "isDeprecated": false,
                    "name": "returnType",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the argument",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The type of the argument",
                        "name": "typeDef",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TypeDefID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A doc string for the argument, if any",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "A default value to use for this argument if not explicitly set by the caller, if any",
                        "name": "defaultValue",
                        "type": {
                          "kind": "SCALAR",
                          "name": "JSON",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns the function with the provided argument",
                    "isDeprecated": false,
                    "name": "withArg",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Function",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The doc string to set.",
                        "name": "description",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns the function with the given doc string.",
                    "isDeprecated": false,
                    "name": "withDescription",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Function",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Function",
                "possibleTypes": []
              },
              {
                "description": "An argument accepted by a function.\n\nThis is a specification for an argument at function definition time, not an argument passed at function call time.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A default value to use for this argument when not explicitly set by the caller, if any.",
                    "isDeprecated": false,
                    "name": "defaultValue",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "JSON",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A doc string for the argument, if any.",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this FunctionArg.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "FunctionArgID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the argument in lowerCamelCase format.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The type of the argument.",
                    "isDeprecated": false,
                    "name": "typeDef",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "FunctionArg",
                "possibleTypes": []
              },
              {
                "description": "The `FunctionArgID` scalar type represents an identifier for an object of type FunctionArg.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "FunctionArgID",
                "possibleTypes": []
              },
              {
                "description": "An active function call.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this FunctionCall.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "FunctionCallID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The argument values the function is being invoked with.",
                    "isDeprecated": false,
                    "name": "inputArgs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "FunctionCallArgValue",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the function being called.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object.",
                    "isDeprecated": false,
                    "name": "parent",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "JSON",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module.",
                    "isDeprecated": false,
                    "name": "parentName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "JSON serialization of the return value.",
                        "name": "value",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "JSON",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Set the return value of the function call to the provided value.",
                    "isDeprecated": false,
                    "name": "returnValue",
                    "type": {
                      "kind": "SCALAR",
                      "name": "Void",
                      "ofType": null
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "FunctionCall",
                "possibleTypes": []
              },
              {
                "description": "A value passed as a named argument to a function call.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this FunctionCallArgValue.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "FunctionCallArgValueID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the argument.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The value of the argument represented as a JSON serialized string.",
                    "isDeprecated": false,
                    "name": "value",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "JSON",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "FunctionCallArgValue",
                "possibleTypes": []
              },
              {
                "description": "The `FunctionCallArgValueID` scalar type represents an identifier for an object of type FunctionCallArgValue.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "FunctionCallArgValueID",
                "possibleTypes": []
              },
              {
                "description": "The `FunctionCallID` scalar type represents an identifier for an object of type FunctionCall.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "FunctionCallID",
                "possibleTypes": []
              },
              {
                "description": "The `FunctionID` scalar type represents an identifier for an object of type Function.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "FunctionID",
                "possibleTypes": []
              },
              {
                "description": "The result of running an SDK's codegen.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The directory containing the generated code.",
                    "isDeprecated": false,
                    "name": "code",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this GeneratedCode.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "GeneratedCodeID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "List of paths to mark generated in version control (i.e. .gitattributes).",
                    "isDeprecated": false,
                    "name": "vcsGeneratedPaths",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "List of paths to ignore in version control (i.e. .gitignore).",
                    "isDeprecated": false,
                    "name": "vcsIgnoredPaths",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "paths",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Set the list of paths to mark generated in version control.",
                    "isDeprecated": false,
                    "name": "withVCSGeneratedPaths",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GeneratedCode",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "paths",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Set the list of paths to ignore in version control.",
                    "isDeprecated": false,
                    "name": "withVCSIgnoredPaths",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GeneratedCode",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "GeneratedCode",
                "possibleTypes": []
              },
              {
                "description": "The `GeneratedCodeID` scalar type represents an identifier for an object of type GeneratedCode.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "GeneratedCodeID",
                "possibleTypes": []
              },
              {
                "description": "Module source originating from a git repo.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The URL from which the source's git repo can be cloned.",
                    "isDeprecated": false,
                    "name": "cloneURL",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The resolved commit of the git repo this source points to.",
                    "isDeprecated": false,
                    "name": "commit",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The directory containing everything needed to load load and use the module.",
                    "isDeprecated": false,
                    "name": "contextDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The URL to the source's git repo in a web browser",
                    "isDeprecated": false,
                    "name": "htmlURL",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this GitModuleSource.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "GitModuleSourceID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory).",
                    "isDeprecated": false,
                    "name": "rootSubpath",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The specified version of the git repo this source points to.",
                    "isDeprecated": false,
                    "name": "version",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "GitModuleSource",
                "possibleTypes": []
              },
              {
                "description": "The `GitModuleSourceID` scalar type represents an identifier for an object of type GitModuleSource.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "GitModuleSourceID",
                "possibleTypes": []
              },
              {
                "description": "A git ref (tag, branch, or commit).",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The resolved commit id at this ref.",
                    "isDeprecated": false,
                    "name": "commit",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this GitRef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "GitRefID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "DEPRECATED: This option should be passed to `git` instead.",
                        "name": "sshKnownHosts",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "DEPRECATED: This option should be passed to `git` instead.",
                        "name": "sshAuthSocket",
                        "type": {
                          "kind": "SCALAR",
                          "name": "SocketID",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "The filesystem tree at this ref.",
                    "isDeprecated": false,
                    "name": "tree",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "GitRef",
                "possibleTypes": []
              },
              {
                "description": "The `GitRefID` scalar type represents an identifier for an object of type GitRef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "GitRefID",
                "possibleTypes": []
              },
              {
                "description": "A git repository.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Branch's name (e.g., \"main\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns details of a branch.",
                    "isDeprecated": false,
                    "name": "branch",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Identifier of the commit (e.g., \"b6315d8f2810962c601af73f86831f6866ea798b\").",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns details of a commit.",
                    "isDeprecated": false,
                    "name": "commit",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Returns details for HEAD.",
                    "isDeprecated": false,
                    "name": "head",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this GitRepository.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "GitRepositoryID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns details of a ref.",
                    "isDeprecated": false,
                    "name": "ref",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Tag's name (e.g., \"v0.3.9\").",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns details of a tag.",
                    "isDeprecated": false,
                    "name": "tag",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Secret used to populate the Authorization HTTP header",
                        "name": "header",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SecretID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Header to authenticate the remote with.",
                    "isDeprecated": false,
                    "name": "withAuthHeader",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRepository",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Secret used to populate the password during basic HTTP Authorization",
                        "name": "token",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SecretID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Token to authenticate the remote with.",
                    "isDeprecated": false,
                    "name": "withAuthToken",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRepository",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "GitRepository",
                "possibleTypes": []
              },
              {
                "description": "The `GitRepositoryID` scalar type represents an identifier for an object of type GitRepository.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "GitRepositoryID",
                "possibleTypes": []
              },
              {
                "description": "Information about the host environment.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the directory to access (e.g., \".\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Exclude artifacts that match the given pattern (e.g., [\"node_modules/\", \".git*\"]).",
                        "name": "exclude",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Include only artifacts that match the given pattern (e.g., [\"app/\", \"package.*\"]).",
                        "name": "include",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "SCALAR",
                              "name": "String",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Accesses a directory on the host.",
                    "isDeprecated": false,
                    "name": "directory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the file to retrieve (e.g., \"README.md\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Accesses a file on the host.",
                    "isDeprecated": false,
                    "name": "file",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Host.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "HostID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "\"localhost\"",
                        "description": "Upstream host to forward traffic to.",
                        "name": "host",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Ports to expose via the service, forwarding through the host network.\n\nIf a port's frontend is unspecified or 0, it defaults to the same as the backend port.\n\nAn empty set of ports is not valid; an error will be returned.",
                        "name": "ports",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "INPUT_OBJECT",
                                "name": "PortForward",
                                "ofType": null
                              }
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a service that forwards traffic to a specified address via the host.",
                    "isDeprecated": false,
                    "name": "service",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Service",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The user defined name for this secret.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Location of the file to set as a secret.",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Sets a secret given a user-defined name and the file path on the host, and returns the secret.\n\nThe file is limited to a size of 512000 bytes.",
                    "isDeprecated": false,
                    "name": "setSecretFile",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Secret",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Service to send traffic from the tunnel.",
                        "name": "service",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ServiceID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "[]",
                        "description": "Configure explicit port forwarding rules for the tunnel.\n\nIf a port's frontend is unspecified or 0, a random port will be chosen by the host.\n\nIf no ports are given, all of the service's ports are forwarded. If native is true, each port maps to the same port on the host. If native is false, each port maps to a random port chosen by the host.\n\nIf ports are given and native is true, the ports are additive.",
                        "name": "ports",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "INPUT_OBJECT",
                              "name": "PortForward",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Map each service port to the same port on the host, as if the service were running natively.\n\nNote: enabling may result in port conflicts.",
                        "name": "native",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a tunnel that forwards traffic from the host to a service.",
                    "isDeprecated": false,
                    "name": "tunnel",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Service",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Location of the Unix socket (e.g., \"/var/run/docker.sock\").",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Accesses a Unix socket on the host.",
                    "isDeprecated": false,
                    "name": "unixSocket",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Socket",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Host",
                "possibleTypes": []
              },
              {
                "description": "The `HostID` scalar type represents an identifier for an object of type Host.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "HostID",
                "possibleTypes": []
              },
              {
                "description": "Compression algorithm to use for image layers.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "Gzip"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "Zstd"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "EStarGZ"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "Uncompressed"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "ImageLayerCompression",
                "possibleTypes": []
              },
              {
                "description": "Mediatypes to use in published or exported image metadata.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "OCIMediaTypes"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "DockerMediaTypes"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "ImageMediaTypes",
                "possibleTypes": []
              },
              {
                "description": "A graphql input type, which is essentially just a group of named args.\nThis is currently only used to represent pre-existing usage of graphql input types\nin the core API. It is not used by user modules and shouldn't ever be as user\nmodule accept input objects via their id rather than graphql input types.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Static fields defined on this input object, if any.",
                    "isDeprecated": false,
                    "name": "fields",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "FieldTypeDef",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this InputTypeDef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "InputTypeDefID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the input object.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "InputTypeDef",
                "possibleTypes": []
              },
              {
                "description": "The `InputTypeDefID` scalar type represents an identifier for an object of type InputTypeDef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "InputTypeDefID",
                "possibleTypes": []
              },
              {
                "description": "The `Int` scalar type represents non-fractional signed whole numeric values. Int can represent values between -(2^31) and 2^31 - 1.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "Int",
                "possibleTypes": []
              },
              {
                "description": "A definition of a custom interface defined in a Module.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The doc string for the interface, if any.",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Functions defined on this interface, if any.",
                    "isDeprecated": false,
                    "name": "functions",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "Function",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this InterfaceTypeDef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "InterfaceTypeDefID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the interface.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise.",
                    "isDeprecated": false,
                    "name": "sourceModuleName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "InterfaceTypeDef",
                "possibleTypes": []
              },
              {
                "description": "The `InterfaceTypeDefID` scalar type represents an identifier for an object of type InterfaceTypeDef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "InterfaceTypeDefID",
                "possibleTypes": []
              },
              {
                "description": "An arbitrary JSON-encoded value.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "JSON",
                "possibleTypes": []
              },
              {
                "description": "A simple key value object that represents a label.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Label.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "LabelID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The label name.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The label value.",
                    "isDeprecated": false,
                    "name": "value",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Label",
                "possibleTypes": []
              },
              {
                "description": "The `LabelID` scalar type represents an identifier for an object of type Label.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "LabelID",
                "possibleTypes": []
              },
              {
                "description": "A definition of a list type in a Module.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The type of the elements in the list.",
                    "isDeprecated": false,
                    "name": "elementTypeDef",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this ListTypeDef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ListTypeDefID",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "ListTypeDef",
                "possibleTypes": []
              },
              {
                "description": "The `ListTypeDefID` scalar type represents an identifier for an object of type ListTypeDef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ListTypeDefID",
                "possibleTypes": []
              },
              {
                "description": "Module source that that originates from a path locally relative to an arbitrary directory.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The directory containing everything needed to load load and use the module.",
                    "isDeprecated": false,
                    "name": "contextDirectory",
                    "type": {
                      "kind": "OBJECT",
                      "name": "Directory",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this LocalModuleSource.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "LocalModuleSourceID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory).",
                    "isDeprecated": false,
                    "name": "rootSubpath",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "LocalModuleSource",
                "possibleTypes": []
              },
              {
                "description": "The `LocalModuleSourceID` scalar type represents an identifier for an object of type LocalModuleSource.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "LocalModuleSourceID",
                "possibleTypes": []
              },
              {
                "description": "A Dagger module.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Modules used by this module.",
                    "isDeprecated": false,
                    "name": "dependencies",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "Module",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The dependencies as configured by the module.",
                    "isDeprecated": false,
                    "name": "dependencyConfig",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "ModuleDependency",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The doc string of the module, if any",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The generated files and directories made on top of the module source's context directory.",
                    "isDeprecated": false,
                    "name": "generatedContextDiff",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The module source's context plus any configuration and source files created by codegen.",
                    "isDeprecated": false,
                    "name": "generatedContextDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Module.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ModuleID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the module with the objects loaded via its SDK.",
                    "isDeprecated": false,
                    "name": "initialize",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Interfaces served by this module.",
                    "isDeprecated": false,
                    "name": "interfaces",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "TypeDef",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the module",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Objects served by this module.",
                    "isDeprecated": false,
                    "name": "objects",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "TypeDef",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.",
                    "isDeprecated": false,
                    "name": "runtime",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The SDK used by this module. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.",
                    "isDeprecated": false,
                    "name": "sdk",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Serve a module's API in the current session.\n\nNote: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.",
                    "isDeprecated": false,
                    "name": "serve",
                    "type": {
                      "kind": "SCALAR",
                      "name": "Void",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The source for the module.",
                    "isDeprecated": false,
                    "name": "source",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The description to set",
                        "name": "description",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves the module with the given description",
                    "isDeprecated": false,
                    "name": "withDescription",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "iface",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "This module plus the given Interface type and associated functions",
                    "isDeprecated": false,
                    "name": "withInterface",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "object",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "This module plus the given Object type and associated functions.",
                    "isDeprecated": false,
                    "name": "withObject",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The module source to initialize from.",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ModuleSourceID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves the module with basic configuration loaded if present.",
                    "isDeprecated": false,
                    "name": "withSource",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Module",
                "possibleTypes": []
              },
              {
                "description": "The configuration of dependency of a module.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this ModuleDependency.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ModuleDependencyID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the dependency module.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The source for the dependency module.",
                    "isDeprecated": false,
                    "name": "source",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "ModuleDependency",
                "possibleTypes": []
              },
              {
                "description": "The `ModuleDependencyID` scalar type represents an identifier for an object of type ModuleDependency.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ModuleDependencyID",
                "possibleTypes": []
              },
              {
                "description": "The `ModuleID` scalar type represents an identifier for an object of type Module.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ModuleID",
                "possibleTypes": []
              },
              {
                "description": "The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If the source is a of kind git, the git source representation of it.",
                    "isDeprecated": false,
                    "name": "asGitSource",
                    "type": {
                      "kind": "OBJECT",
                      "name": "GitModuleSource",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If the source is of kind local, the local source representation of it.",
                    "isDeprecated": false,
                    "name": "asLocalSource",
                    "type": {
                      "kind": "OBJECT",
                      "name": "LocalModuleSource",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation",
                    "isDeprecated": false,
                    "name": "asModule",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A human readable ref string representation of this module source.",
                    "isDeprecated": false,
                    "name": "asString",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Returns whether the module source has a configuration file.",
                    "isDeprecated": false,
                    "name": "configExists",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The directory containing everything needed to load load and use the module.",
                    "isDeprecated": false,
                    "name": "contextDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The dependencies of the module source. Includes dependencies from the configuration and any extras from withDependencies calls.",
                    "isDeprecated": false,
                    "name": "dependencies",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "ModuleDependency",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The path from the source directory to select.",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "The directory containing the module configuration and source code (source code may be in a subdir).",
                    "isDeprecated": false,
                    "name": "directory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this ModuleSource.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ModuleSourceID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The kind of source (e.g. local, git, etc.)",
                    "isDeprecated": false,
                    "name": "kind",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "ENUM",
                        "name": "ModuleSourceKind",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If set, the name of the module this source references, including any overrides at runtime by callers.",
                    "isDeprecated": false,
                    "name": "moduleName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The original name of the module this source references, as defined in the module configuration.",
                    "isDeprecated": false,
                    "name": "moduleOriginalName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The path to the module source's context directory on the caller's filesystem. Only valid for local sources.",
                    "isDeprecated": false,
                    "name": "resolveContextPathFromCaller",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The dependency module source to resolve.",
                        "name": "dep",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ModuleSourceID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Resolve the provided module source arg as a dependency relative to this module source.",
                    "isDeprecated": false,
                    "name": "resolveDependency",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The path on the caller's filesystem to load.",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "If set, the name of the view to apply to the path.",
                        "name": "viewName",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a directory from the caller optionally with a given view applied.",
                    "isDeprecated": false,
                    "name": "resolveDirectoryFromCaller",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Load the source from its path on the caller's filesystem, including only needed+configured files and directories. Only valid for local sources.",
                    "isDeprecated": false,
                    "name": "resolveFromCaller",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The path relative to context of the root of the module source, which contains dagger.json. It also contains the module implementation source code, but that may or may not being a subdir of this root.",
                    "isDeprecated": false,
                    "name": "sourceRootSubpath",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The path relative to context of the module implementation source code.",
                    "isDeprecated": false,
                    "name": "sourceSubpath",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the view to retrieve.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieve a named view defined for this module source.",
                    "isDeprecated": false,
                    "name": "view",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSourceView",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The named views defined for this module source, which are sets of directory filters that can be applied to directory arguments provided to functions.",
                    "isDeprecated": false,
                    "name": "views",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "ModuleSourceView",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The directory to set as the context directory.",
                        "name": "dir",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Update the module source with a new context directory. Only valid for local sources.",
                    "isDeprecated": false,
                    "name": "withContextDirectory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The dependencies to append.",
                        "name": "dependencies",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "ModuleDependencyID",
                                "ofType": null
                              }
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Append the provided dependencies to the module source's dependency list.",
                    "isDeprecated": false,
                    "name": "withDependencies",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name to set.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Update the module source with a new name.",
                    "isDeprecated": false,
                    "name": "withName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The SDK to set.",
                        "name": "sdk",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Update the module source with a new SDK.",
                    "isDeprecated": false,
                    "name": "withSDK",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The path to set as the source subpath.",
                        "name": "path",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Update the module source with a new source subpath.",
                    "isDeprecated": false,
                    "name": "withSourceSubpath",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the view to set.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The patterns to set as the view filters.",
                        "name": "patterns",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "LIST",
                            "name": null,
                            "ofType": {
                              "kind": "NON_NULL",
                              "name": null,
                              "ofType": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                              }
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Update the module source with a new named view.",
                    "isDeprecated": false,
                    "name": "withView",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "ModuleSource",
                "possibleTypes": []
              },
              {
                "description": "The `ModuleSourceID` scalar type represents an identifier for an object of type ModuleSource.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ModuleSourceID",
                "possibleTypes": []
              },
              {
                "description": "The kind of module source.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "LOCAL_SOURCE"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "GIT_SOURCE"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "ModuleSourceKind",
                "possibleTypes": []
              },
              {
                "description": "A named set of path filters that can be applied to directory arguments provided to functions.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this ModuleSourceView.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ModuleSourceViewID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the view",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The patterns of the view used to filter paths",
                    "isDeprecated": false,
                    "name": "patterns",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "ModuleSourceView",
                "possibleTypes": []
              },
              {
                "description": "The `ModuleSourceViewID` scalar type represents an identifier for an object of type ModuleSourceView.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ModuleSourceViewID",
                "possibleTypes": []
              },
              {
                "description": "Transport layer network protocol associated to a port.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "TCP"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "UDP"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "NetworkProtocol",
                "possibleTypes": []
              },
              {
                "description": "A definition of a custom object defined in a Module.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The function used to construct new instances of this object, if any",
                    "isDeprecated": false,
                    "name": "constructor",
                    "type": {
                      "kind": "OBJECT",
                      "name": "Function",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The doc string for the object, if any.",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Static fields defined on this object, if any.",
                    "isDeprecated": false,
                    "name": "fields",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "FieldTypeDef",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Functions defined on this object, if any.",
                    "isDeprecated": false,
                    "name": "functions",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "Function",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this ObjectTypeDef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ObjectTypeDefID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the object.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise.",
                    "isDeprecated": false,
                    "name": "sourceModuleName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "ObjectTypeDef",
                "possibleTypes": []
              },
              {
                "description": "The `ObjectTypeDefID` scalar type represents an identifier for an object of type ObjectTypeDef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ObjectTypeDefID",
                "possibleTypes": []
              },
              {
                "description": "Key value object that represents a pipeline label.",
                "enumValues": [],
                "fields": [],
                "inputFields": [
                  {
                    "defaultValue": null,
                    "description": "Label name.",
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "defaultValue": null,
                    "description": "Label value.",
                    "name": "value",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "interfaces": [],
                "kind": "INPUT_OBJECT",
                "name": "PipelineLabel",
                "possibleTypes": []
              },
              {
                "description": "The platform config OS and architecture in a Container.\n\nThe format is [os]/[platform]/[version] (e.g., \"darwin/arm64/v7\", \"windows/amd64\", \"linux/arm64\").",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "Platform",
                "possibleTypes": []
              },
              {
                "description": "A port exposed by a container.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The port description.",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Skip the health check when run as a service.",
                    "isDeprecated": false,
                    "name": "experimentalSkipHealthcheck",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Port.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "PortID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The port number.",
                    "isDeprecated": false,
                    "name": "port",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Int",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The transport layer protocol.",
                    "isDeprecated": false,
                    "name": "protocol",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "ENUM",
                        "name": "NetworkProtocol",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Port",
                "possibleTypes": []
              },
              {
                "description": "Port forwarding rules for tunneling network traffic.",
                "enumValues": [],
                "fields": [],
                "inputFields": [
                  {
                    "defaultValue": null,
                    "description": "Port to expose to clients. If unspecified, a default will be chosen.",
                    "name": "frontend",
                    "type": {
                      "kind": "SCALAR",
                      "name": "Int",
                      "ofType": null
                    }
                  },
                  {
                    "defaultValue": null,
                    "description": "Destination port for traffic.",
                    "name": "backend",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Int",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "defaultValue": "TCP",
                    "description": "Transport layer protocol to use for traffic.",
                    "name": "protocol",
                    "type": {
                      "kind": "ENUM",
                      "name": "NetworkProtocol",
                      "ofType": null
                    }
                  }
                ],
                "interfaces": [],
                "kind": "INPUT_OBJECT",
                "name": "PortForward",
                "possibleTypes": []
              },
              {
                "description": "The `PortID` scalar type represents an identifier for an object of type Port.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "PortID",
                "possibleTypes": []
              },
              {
                "description": "The root of the DAG.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Digest of the blob",
                        "name": "digest",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Size of the blob",
                        "name": "size",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "Int",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Media type of the blob",
                        "name": "mediaType",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Digest of the uncompressed blob",
                        "name": "uncompressed",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves a content-addressed blob.",
                    "isDeprecated": false,
                    "name": "blob",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Digest of the image manifest",
                        "name": "digest",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves a container builtin to the engine.",
                    "isDeprecated": false,
                    "name": "builtinContainer",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "A string identifier to target this cache volume (e.g., \"modules-cache\").",
                        "name": "key",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Constructs a cache volume for a given cache key.",
                    "isDeprecated": false,
                    "name": "cacheVolume",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "CacheVolume",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Version required by the SDK.",
                        "name": "version",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Checks if the current Dagger Engine is compatible with an SDK's required version.",
                    "isDeprecated": false,
                    "name": "checkVersionCompatibility",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "DEPRECATED: Use `loadContainerFromID` instead.",
                        "name": "id",
                        "type": {
                          "kind": "SCALAR",
                          "name": "ContainerID",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Platform to initialize the container with.",
                        "name": "platform",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Platform",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a scratch container.\n\nOptional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.",
                    "isDeprecated": false,
                    "name": "container",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The FunctionCall context that the SDK caller is currently executing in.\n\nIf the caller is not currently executing in a function, this will return an error.",
                    "isDeprecated": false,
                    "name": "currentFunctionCall",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "FunctionCall",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The module currently being served in the session, if any.",
                    "isDeprecated": false,
                    "name": "currentModule",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "CurrentModule",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The TypeDef representations of the objects currently being served in the session.",
                    "isDeprecated": false,
                    "name": "currentTypeDefs",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "TypeDef",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The default platform of the engine.",
                    "isDeprecated": false,
                    "name": "defaultPlatform",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Platform",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "DEPRECATED: Use `loadDirectoryFromID` instead.",
                        "name": "id",
                        "type": {
                          "kind": "SCALAR",
                          "name": "DirectoryID",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates an empty directory.",
                    "isDeprecated": false,
                    "name": "directory",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FileID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": "Use `loadFileFromID` instead.",
                    "description": "",
                    "isDeprecated": true,
                    "name": "file",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Name of the function, in its original format from the implementation language.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Return type of the function.",
                        "name": "returnType",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a function.",
                    "isDeprecated": false,
                    "name": "function",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Function",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "code",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Create a code generation result, given a directory containing the generated code.",
                    "isDeprecated": false,
                    "name": "generatedCode",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GeneratedCode",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "URL of the git repository.\n\nCan be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.\n\nSuffix \".git\" is optional.",
                        "name": "url",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Set to true to keep .git directory.",
                        "name": "keepGitDir",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "A service which must be started before the repo is fetched.",
                        "name": "experimentalServiceHost",
                        "type": {
                          "kind": "SCALAR",
                          "name": "ServiceID",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Set SSH known hosts",
                        "name": "sshKnownHosts",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Set SSH auth socket",
                        "name": "sshAuthSocket",
                        "type": {
                          "kind": "SCALAR",
                          "name": "SocketID",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Queries a Git repository.",
                    "isDeprecated": false,
                    "name": "git",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRepository",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Queries the host environment.",
                    "isDeprecated": false,
                    "name": "host",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Host",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "HTTP url to get the content from (e.g., \"https://docs.dagger.io\").",
                        "name": "url",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "A service which must be started before the URL is fetched.",
                        "name": "experimentalServiceHost",
                        "type": {
                          "kind": "SCALAR",
                          "name": "ServiceID",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a file containing an http remote url content.",
                    "isDeprecated": false,
                    "name": "http",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "CacheVolumeID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a CacheVolume from its ID.",
                    "isDeprecated": false,
                    "name": "loadCacheVolumeFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "CacheVolume",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ContainerID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Container from its ID.",
                    "isDeprecated": false,
                    "name": "loadContainerFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Container",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "CurrentModuleID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a CurrentModule from its ID.",
                    "isDeprecated": false,
                    "name": "loadCurrentModuleFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "CurrentModule",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "DirectoryID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Directory from its ID.",
                    "isDeprecated": false,
                    "name": "loadDirectoryFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Directory",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "EnvVariableID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a EnvVariable from its ID.",
                    "isDeprecated": false,
                    "name": "loadEnvVariableFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "EnvVariable",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FieldTypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a FieldTypeDef from its ID.",
                    "isDeprecated": false,
                    "name": "loadFieldTypeDefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "FieldTypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FileID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a File from its ID.",
                    "isDeprecated": false,
                    "name": "loadFileFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "File",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FunctionArgID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a FunctionArg from its ID.",
                    "isDeprecated": false,
                    "name": "loadFunctionArgFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "FunctionArg",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FunctionCallArgValueID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a FunctionCallArgValue from its ID.",
                    "isDeprecated": false,
                    "name": "loadFunctionCallArgValueFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "FunctionCallArgValue",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FunctionCallID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a FunctionCall from its ID.",
                    "isDeprecated": false,
                    "name": "loadFunctionCallFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "FunctionCall",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FunctionID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Function from its ID.",
                    "isDeprecated": false,
                    "name": "loadFunctionFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Function",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "GeneratedCodeID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a GeneratedCode from its ID.",
                    "isDeprecated": false,
                    "name": "loadGeneratedCodeFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GeneratedCode",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "GitModuleSourceID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a GitModuleSource from its ID.",
                    "isDeprecated": false,
                    "name": "loadGitModuleSourceFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "GitRefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a GitRef from its ID.",
                    "isDeprecated": false,
                    "name": "loadGitRefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "GitRepositoryID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a GitRepository from its ID.",
                    "isDeprecated": false,
                    "name": "loadGitRepositoryFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "GitRepository",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "HostID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Host from its ID.",
                    "isDeprecated": false,
                    "name": "loadHostFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Host",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "InputTypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a InputTypeDef from its ID.",
                    "isDeprecated": false,
                    "name": "loadInputTypeDefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "InputTypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "InterfaceTypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a InterfaceTypeDef from its ID.",
                    "isDeprecated": false,
                    "name": "loadInterfaceTypeDefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "InterfaceTypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "LabelID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Label from its ID.",
                    "isDeprecated": false,
                    "name": "loadLabelFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Label",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ListTypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a ListTypeDef from its ID.",
                    "isDeprecated": false,
                    "name": "loadListTypeDefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ListTypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "LocalModuleSourceID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a LocalModuleSource from its ID.",
                    "isDeprecated": false,
                    "name": "loadLocalModuleSourceFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "LocalModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ModuleDependencyID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a ModuleDependency from its ID.",
                    "isDeprecated": false,
                    "name": "loadModuleDependencyFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleDependency",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ModuleID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Module from its ID.",
                    "isDeprecated": false,
                    "name": "loadModuleFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ModuleSourceID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a ModuleSource from its ID.",
                    "isDeprecated": false,
                    "name": "loadModuleSourceFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ModuleSourceViewID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a ModuleSourceView from its ID.",
                    "isDeprecated": false,
                    "name": "loadModuleSourceViewFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSourceView",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ObjectTypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a ObjectTypeDef from its ID.",
                    "isDeprecated": false,
                    "name": "loadObjectTypeDefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ObjectTypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "PortID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Port from its ID.",
                    "isDeprecated": false,
                    "name": "loadPortFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Port",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ScalarTypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a ScalarTypeDef from its ID.",
                    "isDeprecated": false,
                    "name": "loadScalarTypeDefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ScalarTypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SecretID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Secret from its ID.",
                    "isDeprecated": false,
                    "name": "loadSecretFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Secret",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ServiceID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Service from its ID.",
                    "isDeprecated": false,
                    "name": "loadServiceFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Service",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SocketID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Socket from its ID.",
                    "isDeprecated": false,
                    "name": "loadSocketFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Socket",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TerminalID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a Terminal from its ID.",
                    "isDeprecated": false,
                    "name": "loadTerminalFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Terminal",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Load a TypeDef from its ID.",
                    "isDeprecated": false,
                    "name": "loadTypeDefFromID",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Create a new module.",
                    "isDeprecated": false,
                    "name": "module",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Module",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The source of the dependency",
                        "name": "source",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "ModuleSourceID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "If set, the name to use for the dependency. Otherwise, once installed to a parent module, the name of the dependency module will be used by default.",
                        "name": "name",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Create a new module dependency configuration from a module source and name",
                    "isDeprecated": false,
                    "name": "moduleDependency",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleDependency",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The string ref representation of the module source",
                        "name": "refString",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "If true, enforce that the source is a stable version for source kinds that support versioning.",
                        "name": "stable",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Create a new module source instance from a source ref string.",
                    "isDeprecated": false,
                    "name": "moduleSource",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "ModuleSource",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "Name of the sub-pipeline.",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Description of the sub-pipeline.",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "Labels to apply to the sub-pipeline.",
                        "name": "labels",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "INPUT_OBJECT",
                              "name": "PipelineLabel",
                              "ofType": null
                            }
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a named sub-pipeline.",
                    "isDeprecated": false,
                    "name": "pipeline",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Query",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "accessor",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Reference a secret by name.",
                    "isDeprecated": false,
                    "name": "secret",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Secret",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The user defined name for this secret",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The plaintext of the secret",
                        "name": "plaintext",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Sets a secret given a user defined name to its plaintext and returns the secret.\n\nThe plaintext value is limited to a size of 128000 bytes.",
                    "isDeprecated": false,
                    "name": "setSecret",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Secret",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "id",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "SocketID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": "Use `loadSocketFromID` instead.",
                    "description": "Loads a socket by its ID.",
                    "isDeprecated": true,
                    "name": "socket",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "Socket",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Create a new TypeDef.",
                    "isDeprecated": false,
                    "name": "typeDef",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Get the current Dagger Engine version.",
                    "isDeprecated": false,
                    "name": "version",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Query",
                "possibleTypes": []
              },
              {
                "description": "A definition of a custom scalar defined in a Module.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A doc string for the scalar, if any.",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this ScalarTypeDef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ScalarTypeDefID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of the scalar.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If this ScalarTypeDef is associated with a Module, the name of the module. Unset otherwise.",
                    "isDeprecated": false,
                    "name": "sourceModuleName",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "ScalarTypeDef",
                "possibleTypes": []
              },
              {
                "description": "The `ScalarTypeDefID` scalar type represents an identifier for an object of type ScalarTypeDef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ScalarTypeDefID",
                "possibleTypes": []
              },
              {
                "description": "A reference to a secret value, which can be handled more safely than the value itself.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Secret.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "SecretID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The name of this secret.",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The value of this secret.",
                    "isDeprecated": false,
                    "name": "plaintext",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Secret",
                "possibleTypes": []
              },
              {
                "description": "The `SecretID` scalar type represents an identifier for an object of type Secret.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "SecretID",
                "possibleTypes": []
              },
              {
                "description": "A content-addressed service providing TCP connectivity.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The exposed port number for the endpoint",
                        "name": "port",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Int",
                          "ofType": null
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "Return a URL with the given scheme, eg. http for http://",
                        "name": "scheme",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Retrieves an endpoint that clients can use to reach this container.\n\nIf no port is specified, the first exposed port is used. If none exist an error is returned.\n\nIf a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.",
                    "isDeprecated": false,
                    "name": "endpoint",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves a hostname which can be used by clients to reach this container.",
                    "isDeprecated": false,
                    "name": "hostname",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Service.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ServiceID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Retrieves the list of ports provided by the service.",
                    "isDeprecated": false,
                    "name": "ports",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "Port",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Start the service and wait for its health checks to succeed.\n\nServices bound to a Container do not need to be manually started.",
                    "isDeprecated": false,
                    "name": "start",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ServiceID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "false",
                        "description": "Immediately kill the service without waiting for a graceful exit",
                        "name": "kill",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Stop the service.",
                    "isDeprecated": false,
                    "name": "stop",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "ServiceID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "[]",
                        "description": "List of frontend/backend port mappings to forward.\n\nFrontend is the port accepting traffic on the host, backend is the service port.",
                        "name": "ports",
                        "type": {
                          "kind": "LIST",
                          "name": null,
                          "ofType": {
                            "kind": "NON_NULL",
                            "name": null,
                            "ofType": {
                              "kind": "INPUT_OBJECT",
                              "name": "PortForward",
                              "ofType": null
                            }
                          }
                        }
                      },
                      {
                        "defaultValue": "false",
                        "description": "Bind each tunnel port to a random port on the host.",
                        "name": "random",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Creates a tunnel that forwards traffic from the caller's network to this service.",
                    "isDeprecated": false,
                    "name": "up",
                    "type": {
                      "kind": "SCALAR",
                      "name": "Void",
                      "ofType": null
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Service",
                "possibleTypes": []
              },
              {
                "description": "The `ServiceID` scalar type represents an identifier for an object of type Service.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "ServiceID",
                "possibleTypes": []
              },
              {
                "description": "A Unix or TCP/IP socket that can be mounted into a container.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Socket.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "SocketID",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Socket",
                "possibleTypes": []
              },
              {
                "description": "The `SocketID` scalar type represents an identifier for an object of type Socket.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "SocketID",
                "possibleTypes": []
              },
              {
                "description": "The `String` scalar type represents textual data, represented as UTF-8 character sequences. The String type is most often used by GraphQL to represent free-form human-readable text.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "String",
                "possibleTypes": []
              },
              {
                "description": "An interactive terminal that clients can connect to.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this Terminal.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "TerminalID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "An http endpoint at which this terminal can be connected to over a websocket.",
                    "isDeprecated": false,
                    "name": "websocketEndpoint",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "Terminal",
                "possibleTypes": []
              },
              {
                "description": "The `TerminalID` scalar type represents an identifier for an object of type Terminal.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "TerminalID",
                "possibleTypes": []
              },
              {
                "description": "A definition of a parameter or return type in a Module.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.",
                    "isDeprecated": false,
                    "name": "asInput",
                    "type": {
                      "kind": "OBJECT",
                      "name": "InputTypeDef",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.",
                    "isDeprecated": false,
                    "name": "asInterface",
                    "type": {
                      "kind": "OBJECT",
                      "name": "InterfaceTypeDef",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.",
                    "isDeprecated": false,
                    "name": "asList",
                    "type": {
                      "kind": "OBJECT",
                      "name": "ListTypeDef",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.",
                    "isDeprecated": false,
                    "name": "asObject",
                    "type": {
                      "kind": "OBJECT",
                      "name": "ObjectTypeDef",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "If kind is SCALAR, the scalar-specific type definition. If kind is not SCALAR, this will be null.",
                    "isDeprecated": false,
                    "name": "asScalar",
                    "type": {
                      "kind": "OBJECT",
                      "name": "ScalarTypeDef",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "A unique identifier for this TypeDef.",
                    "isDeprecated": false,
                    "name": "id",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "TypeDefID",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "The kind of type this is (e.g. primitive, list, object).",
                    "isDeprecated": false,
                    "name": "kind",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "ENUM",
                        "name": "TypeDefKind",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "Whether this type can be set to null. Defaults to false.",
                    "isDeprecated": false,
                    "name": "optional",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "function",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FunctionID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.",
                    "isDeprecated": false,
                    "name": "withConstructor",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "The name of the field in the object",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": null,
                        "description": "The type of the field",
                        "name": "typeDef",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TypeDefID",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "A doc string for the field, if any",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Adds a static field for an Object TypeDef, failing if the type is not an object.",
                    "isDeprecated": false,
                    "name": "withField",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "function",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "FunctionID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.",
                    "isDeprecated": false,
                    "name": "withFunction",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a TypeDef of kind Interface with the provided name.",
                    "isDeprecated": false,
                    "name": "withInterface",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "kind",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "ENUM",
                            "name": "TypeDefKind",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Sets the kind of the type.",
                    "isDeprecated": false,
                    "name": "withKind",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "elementType",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "TypeDefID",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a TypeDef of kind List with the provided type for its elements.",
                    "isDeprecated": false,
                    "name": "withListOf",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a TypeDef of kind Object with the provided name.\n\nNote that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.",
                    "isDeprecated": false,
                    "name": "withObject",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "optional",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "Boolean",
                            "ofType": null
                          }
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Sets whether this type can be set to null.",
                    "isDeprecated": false,
                    "name": "withOptional",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": null,
                        "description": "",
                        "name": "name",
                        "type": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "SCALAR",
                            "name": "String",
                            "ofType": null
                          }
                        }
                      },
                      {
                        "defaultValue": "\"\"",
                        "description": "",
                        "name": "description",
                        "type": {
                          "kind": "SCALAR",
                          "name": "String",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "Returns a TypeDef of kind Scalar with the provided name.",
                    "isDeprecated": false,
                    "name": "withScalar",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "TypeDef",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "TypeDef",
                "possibleTypes": []
              },
              {
                "description": "The `TypeDefID` scalar type represents an identifier for an object of type TypeDef.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "TypeDefID",
                "possibleTypes": []
              },
              {
                "description": "Distinguishes the different kinds of TypeDefs.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "A string value.",
                    "isDeprecated": false,
                    "name": "STRING_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "An integer value.",
                    "isDeprecated": false,
                    "name": "INTEGER_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "A boolean value.",
                    "isDeprecated": false,
                    "name": "BOOLEAN_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "A scalar value of any basic kind.",
                    "isDeprecated": false,
                    "name": "SCALAR_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "A list of values all having the same type.\n\nAlways paired with a ListTypeDef.",
                    "isDeprecated": false,
                    "name": "LIST_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "A named type defined in the GraphQL schema, with fields and functions.\n\nAlways paired with an ObjectTypeDef.",
                    "isDeprecated": false,
                    "name": "OBJECT_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "A named type of functions that can be matched+implemented by other objects+interfaces.\n\nAlways paired with an InterfaceTypeDef.",
                    "isDeprecated": false,
                    "name": "INTERFACE_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "A graphql input type, used only when representing the core API via TypeDefs.",
                    "isDeprecated": false,
                    "name": "INPUT_KIND"
                  },
                  {
                    "deprecationReason": null,
                    "description": "A special kind used to signify that no value is returned.\n\nThis is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.",
                    "isDeprecated": false,
                    "name": "VOID_KIND"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "TypeDefKind",
                "possibleTypes": []
              },
              {
                "description": "The absence of a value.\n\nA Null Void is used as a placeholder for resolvers that do not return anything.",
                "enumValues": [],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "SCALAR",
                "name": "Void",
                "possibleTypes": []
              },
              {
                "description": "A GraphQL schema directive.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "args",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__InputValue",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "locations",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "ENUM",
                            "name": "__DirectiveLocation",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "__Directive",
                "possibleTypes": []
              },
              {
                "description": "A location that a directive may be applied.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "QUERY"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "MUTATION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "SUBSCRIPTION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "FIELD"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "FRAGMENT_DEFINITION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "FRAGMENT_SPREAD"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "INLINE_FRAGMENT"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "VARIABLE_DEFINITION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "SCHEMA"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "SCALAR"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "OBJECT"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "FIELD_DEFINITION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "ARGUMENT_DEFINITION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "INTERFACE"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "UNION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "ENUM"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "ENUM_VALUE"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "INPUT_OBJECT"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "INPUT_FIELD_DEFINITION"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "__DirectiveLocation",
                "possibleTypes": []
              },
              {
                "description": "A possible value of a GraphQL enum.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "deprecationReason",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "isDeprecated",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "__EnumValue",
                "possibleTypes": []
              },
              {
                "description": "A GraphQL object or input field.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "args",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__InputValue",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "deprecationReason",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "isDeprecated",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "type",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "__Type",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "__Field",
                "possibleTypes": []
              },
              {
                "description": "A GraphQL schema input field or argument.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "defaultValue",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "deprecationReason",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "isDeprecated",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "Boolean",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "type",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "__Type",
                        "ofType": null
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "__InputValue",
                "possibleTypes": []
              },
              {
                "description": "A GraphQL schema definition.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "directives",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__Directive",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "mutationType",
                    "type": {
                      "kind": "OBJECT",
                      "name": "__Type",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "queryType",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "OBJECT",
                        "name": "__Type",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "subscriptionType",
                    "type": {
                      "kind": "OBJECT",
                      "name": "__Type",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "types",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__Type",
                            "ofType": null
                          }
                        }
                      }
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "__Schema",
                "possibleTypes": []
              },
              {
                "description": "A GraphQL schema type.",
                "enumValues": [],
                "fields": [
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "description",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "SCALAR",
                        "name": "String",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "false",
                        "description": "",
                        "name": "includeDeprecated",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "enumValues",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__EnumValue",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [
                      {
                        "defaultValue": "false",
                        "description": "",
                        "name": "includeDeprecated",
                        "type": {
                          "kind": "SCALAR",
                          "name": "Boolean",
                          "ofType": null
                        }
                      }
                    ],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "fields",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__Field",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "inputFields",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__InputValue",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "interfaces",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__Type",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "kind",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "ENUM",
                        "name": "__TypeKind",
                        "ofType": null
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "name",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "ofType",
                    "type": {
                      "kind": "OBJECT",
                      "name": "__Type",
                      "ofType": null
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "possibleTypes",
                    "type": {
                      "kind": "NON_NULL",
                      "name": null,
                      "ofType": {
                        "kind": "LIST",
                        "name": null,
                        "ofType": {
                          "kind": "NON_NULL",
                          "name": null,
                          "ofType": {
                            "kind": "OBJECT",
                            "name": "__Type",
                            "ofType": null
                          }
                        }
                      }
                    }
                  },
                  {
                    "args": [],
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "specifiedByURL",
                    "type": {
                      "kind": "SCALAR",
                      "name": "String",
                      "ofType": null
                    }
                  }
                ],
                "inputFields": [],
                "interfaces": [],
                "kind": "OBJECT",
                "name": "__Type",
                "possibleTypes": []
              },
              {
                "description": "The kind of a GraphQL type.",
                "enumValues": [
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "SCALAR"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "OBJECT"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "INTERFACE"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "UNION"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "ENUM"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "INPUT_OBJECT"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "LIST"
                  },
                  {
                    "deprecationReason": null,
                    "description": "",
                    "isDeprecated": false,
                    "name": "NON_NULL"
                  }
                ],
                "fields": [],
                "inputFields": [],
                "interfaces": [],
                "kind": "ENUM",
                "name": "__TypeKind",
                "possibleTypes": []
              }
            ]
          }
        }
        """;
}
