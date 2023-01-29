mod codegen;
mod handlers;
mod models;
mod predicates;

#[cfg(test)]
mod tests {
    use graphql_introspection_query::introspection_response::IntrospectionResponse;

    use crate::codegen::CodeGeneration;

    use pretty_assertions::assert_eq;

    #[test]
    fn can_generate_from_schema() {
        let schema: IntrospectionResponse = serde_json::from_str(INTROSPECTION_QUERY).unwrap();
        let code = CodeGeneration::new().generate(&schema).unwrap();
        assert_eq!("some-code", code);
    }

    const INTROSPECTION_QUERY: &str = r#"{
    "data": {
        "__schema": {
            "directives": [
                {
                    "args": [
                        {
                            "defaultValue": null,
                            "description": "Included when true.",
                            "name": "if",
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
                    "description": "Directs the executor to include this field or fragment only when the `if` argument is true.",
                    "locations": [
                        "FIELD",
                        "FRAGMENT_SPREAD",
                        "INLINE_FRAGMENT"
                    ],
                    "name": "include"
                },
                {
                    "args": [
                        {
                            "defaultValue": null,
                            "description": "Skipped when true.",
                            "name": "if",
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
                    "description": "Directs the executor to skip this field or fragment when the `if` argument is true.",
                    "locations": [
                        "FIELD",
                        "FRAGMENT_SPREAD",
                        "INLINE_FRAGMENT"
                    ],
                    "name": "skip"
                },
                {
                    "args": [
                        {
                            "defaultValue": "\"No longer supported\"",
                            "description": "Explains why this element was deprecated, usually also including a suggestion for how to access supported similar data. Formattedin [Markdown](https://daringfireball.net/projects/markdown/).",
                            "name": "reason",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                            }
                        }
                    ],
                    "description": "Marks an element of a GraphQL schema as no longer supported.",
                    "locations": [
                        "FIELD_DEFINITION",
                        "ENUM_VALUE"
                    ],
                    "name": "deprecated"
                },
                {
                    "args": [],
                    "description": "Hide a field, useful when generating types from the AST where the backend type has more fields than the graphql type",
                    "locations": [
                        "FIELD_DEFINITION"
                    ],
                    "name": "hide"
                }
            ],
            "mutationType": null,
            "queryType": {
                "name": "Query"
            },
            "subscriptionType": null,
            "types": [
                {
                    "description": "A directory.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "Path to the Dockerfile to use.\nDefaults to './Dockerfile'.",
                                    "name": "dockerfile",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
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
                                    "defaultValue": null,
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
                                    "defaultValue": null,
                                    "description": "Target build stage to build.",
                                    "name": "target",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                            "args": [],
                            "deprecationReason": null,
                            "description": "The content-addressed identifier of the directory.",
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
                                    "description": "",
                                    "name": "configPath",
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
                            "description": "load a project's metadata",
                            "isDeprecated": false,
                            "name": "loadProject",
                            "type": {
                                "kind": "NON_NULL",
                                "name": null,
                                "ofType": {
                                    "kind": "OBJECT",
                                    "name": "Project",
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
                                    "name": "description",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
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
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                                    "description": "",
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
                                    "defaultValue": null,
                                    "description": "Exclude artifacts that match the given pattern.\n(e.g. [\"node_modules/\", \".git*\"]).",
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
                                    "defaultValue": null,
                                    "description": "Include only artifacts that match the given pattern.\n(e.g. [\"app/\", \"package.*\"]).",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "defaultValue": null,
                                    "description": "",
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
                                    "description": "",
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
                            "description": "Retrieves this directory with all file/dir timestamps set to the given time, in seconds from the Unix epoch.",
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
                                    "description": "",
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
                                    "description": "",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Directory",
                    "possibleTypes": null
                },
                {
                    "description": "A set of scripts and/or extensions",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "extensions in this project",
                            "isDeprecated": false,
                            "name": "extensions",
                            "type": {
                                "kind": "LIST",
                                "name": null,
                                "ofType": {
                                    "kind": "NON_NULL",
                                    "name": null,
                                    "ofType": {
                                        "kind": "OBJECT",
                                        "name": "Project",
                                        "ofType": null
                                    }
                                }
                            }
                        },
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "Code files generated by the SDKs in the project",
                            "isDeprecated": false,
                            "name": "generatedCode",
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
                            "description": "install the project's schema",
                            "isDeprecated": false,
                            "name": "install",
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
                            "description": "name of the project",
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
                            "description": "schema provided by the project",
                            "isDeprecated": false,
                            "name": "schema",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                            }
                        },
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "sdk used to generate code for and/or execute this project",
                            "isDeprecated": false,
                            "name": "sdk",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                            }
                        }
                    ],
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Project",
                    "possibleTypes": null
                },
                {
                    "description": "Arguments provided to Fields or Directives and the input fields of an InputObject are represented as Input Values which describe their type and optionally a default value.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "A GraphQL-formatted string representing the default value for this input value.",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "__InputValue",
                    "possibleTypes": null
                },
                {
                    "description": "The fundamental unit of any GraphQL Schema is the type. There are many kinds of types in GraphQL as represented by the `__TypeKind` enum.\n\nDepending on the kind of a type, certain fields describe information about that type. Scalar types provide no information beyond a name and description, while Enum types provide their values. Object and Interface types provide the fields they describe. Abstract types, Union and Interface, provide the Object types possible at runtime. List and NonNull types compose other types.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "",
                            "isDeprecated": false,
                            "name": "description",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
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
                        },
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "",
                            "isDeprecated": false,
                            "name": "inputFields",
                            "type": {
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
                        },
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "",
                            "isDeprecated": false,
                            "name": "interfaces",
                            "type": {
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
                    ],
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "__Type",
                    "possibleTypes": null
                },
                {
                    "description": "The `Float` scalar type represents signed double-precision fractional values as specified by [IEEE 754](http://en.wikipedia.org/wiki/IEEE_floating_point). ",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "Float",
                    "possibleTypes": null
                },
                {
                    "description": "Information about the host execution environment.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                                    "description": "",
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
                                    "defaultValue": null,
                                    "description": "",
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
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Accesses an environment variable on the host.",
                            "isDeprecated": false,
                            "name": "envVariable",
                            "type": {
                                "kind": "OBJECT",
                                "name": "HostVariable",
                                "ofType": null
                            }
                        },
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                        },
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                                    "defaultValue": null,
                                    "description": "",
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
                            "deprecationReason": "Use `directory` with path set to '.' instead.",
                            "description": "Retrieves the current working directory on the host.",
                            "isDeprecated": true,
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
                        }
                    ],
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Host",
                    "possibleTypes": null
                },
                {
                    "description": "A global cache volume identifier.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "CacheID",
                    "possibleTypes": null
                },
                {
                    "description": "A git repository.",
                    "enumValues": null,
                    "fields": [
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
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Returns details on one branch.",
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
                            "args": [],
                            "deprecationReason": null,
                            "description": "Lists of branches on the repository.",
                            "isDeprecated": false,
                            "name": "branches",
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
                            "description": "Returns details on one commit.",
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
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Returns details on one tag.",
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
                            "args": [],
                            "deprecationReason": null,
                            "description": "Lists of tags on the repository.",
                            "isDeprecated": false,
                            "name": "tags",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "GitRepository",
                    "possibleTypes": null
                },
                {
                    "description": "A content-addressed directory identifier.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "DirectoryID",
                    "possibleTypes": null
                },
                {
                    "description": "A file identifier.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "FileID",
                    "possibleTypes": null
                },
                {
                    "description": "Object and Interface types are described by a list of Fields, each of which has a name, potentially a list of arguments, and a return type.",
                    "enumValues": null,
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "__Field",
                    "possibleTypes": null
                },
                {
                    "description": "The `ID` scalar type represents a unique identifier, often used to refetch an object or as key for a cache. The ID type appears in a JSON response as a String; however, it is not intended to be human-readable. When expected as an input type, any string (such as `\"4\"`) or integer (such as `4`) input value will be accepted as an ID.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "ID",
                    "possibleTypes": null
                },
                {
                    "description": "A unique identifier for a secret.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "SecretID",
                    "possibleTypes": null
                },
                {
                    "description": "An OCI-compatible container, also known as a docker container.",
                    "enumValues": null,
                    "fields": [
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
                                    "defaultValue": null,
                                    "description": "Path to the Dockerfile to use.\nDefaults to './Dockerfile'.",
                                    "name": "dockerfile",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
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
                                    "defaultValue": null,
                                    "description": "Target build stage to build.",
                                    "name": "target",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Initializes this container from a Dockerfile build, using the context, a dockerfile file path and some additional buildArgs.",
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
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                            "description": "Retrieves a directory at the given path. Mounts are included.",
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
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "Command to run instead of the container's default command.",
                                    "name": "args",
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
                                    "defaultValue": null,
                                    "description": "Content to write to the command's standard input before closing.",
                                    "name": "stdin",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "Redirect the command's standard output to a file in the container.",
                                    "name": "redirectStdout",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "Redirect the command's standard error to a file in the container.",
                                    "name": "redirectStderr",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "Provide dagger access to the executed command.\nDo not use this option unless you trust the command being executed.\nThe command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.",
                                    "name": "experimentalPrivilegedNesting",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "Boolean",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": "Replaced by `withExec`.",
                            "description": "Retrieves this container after executing the specified command inside it.",
                            "isDeprecated": true,
                            "name": "exec",
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
                            "description": "Exit code of the last executed command. Zero means success.\nNull if no command has been executed.",
                            "isDeprecated": false,
                            "name": "exitCode",
                            "type": {
                                "kind": "SCALAR",
                                "name": "Int",
                                "ofType": null
                            }
                        },
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "Host's destination path.\nPath can be relative to the engine's workdir or absolute.",
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
                                    "description": "Identifiers for other platform specific containers.\nUsed for multi-platform image.",
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
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Writes the container as an OCI tarball to the destination file path on the host for the specified platformVariants.\nReturn true on success.",
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
                                    "description": "",
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
                            "description": "Retrieves a file at the given path. Mounts are included.",
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
                                    "description": "Image's address from its registry.\nFormatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).",
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
                            "description": "Initializes this container from the base image published at the given address.",
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
                            "deprecationReason": "Replaced by `rootfs`.",
                            "description": "Retrieves this container's root filesystem. Mounts are not included.",
                            "isDeprecated": true,
                            "name": "fs",
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
                            "description": "A unique identifier for this container.",
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
                                    "name": "description",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Creates a named sub-pipeline",
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
                                    "description": "Registry's address to publish the image to.\nFormatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).",
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
                                    "description": "Identifiers for other platform specific containers.\nUsed for multi-platform image.",
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
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Publishes this container as a new image to the specified address, for the platformVariants, returning a fully qualified ref.",
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
                            "description": "The error stream of the last executed command.\nNull if no command has been executed.",
                            "isDeprecated": false,
                            "name": "stderr",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                            }
                        },
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "The output stream of the last executed command.\nNull if no command has been executed.",
                            "isDeprecated": false,
                            "name": "stdout",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                            }
                        },
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "Retrieves the user to be set for all commands.",
                            "isDeprecated": false,
                            "name": "user",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                            }
                        },
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
                                    "name": "args",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "defaultValue": null,
                                    "description": "",
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
                                    "defaultValue": null,
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "Command to run instead of the container's default command.",
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
                                    "defaultValue": null,
                                    "description": "Content to write to the command's standard input before closing.",
                                    "name": "stdin",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "Redirect the command's standard output to a file in the container.",
                                    "name": "redirectStdout",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "Redirect the command's standard error to a file in the container.",
                                    "name": "redirectStderr",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "Provide dagger access to the executed command.\nDo not use this option unless you trust the command being executed.\nThe command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.",
                                    "name": "experimentalPrivilegedNesting",
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
                            "deprecationReason": "Replaced by `withRootfs`.",
                            "description": "Initializes this container from this DirectoryID.",
                            "isDeprecated": true,
                            "name": "withFS",
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
                                    "description": "",
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
                                    "description": "",
                                    "name": "permissions",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "Int",
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
                                    "description": "",
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
                                    "description": "",
                                    "name": "cache",
                                    "type": {
                                        "kind": "NON_NULL",
                                        "name": null,
                                        "ofType": {
                                            "kind": "SCALAR",
                                            "name": "CacheID",
                                            "ofType": null
                                        }
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "",
                                    "name": "source",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "DirectoryID",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                            "description": "Retrieves this container plus a temporary directory mounted at the given path.",
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
                                    "description": "",
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
                                    "description": "",
                                    "name": "contents",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "",
                                    "name": "permissions",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "Int",
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
                            "description": "Initializes this container from this DirectoryID.",
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
                                    "description": "",
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
                                    "description": "",
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
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Retrieves this containers with a different command user.",
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
                                    "description": "",
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
                                    "description": "",
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
                                    "description": "",
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
                            "description": "Retrieves the working directory for all commands.",
                            "isDeprecated": false,
                            "name": "workdir",
                            "type": {
                                "kind": "SCALAR",
                                "name": "String",
                                "ofType": null
                            }
                        }
                    ],
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Container",
                    "possibleTypes": null
                },
                {
                    "description": "A simple key value object that represents an environment variable.",
                    "enumValues": null,
                    "fields": [
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "EnvVariable",
                    "possibleTypes": null
                },
                {
                    "description": "An environment variable on the host environment.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "A secret referencing the value of this variable.",
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
                            "args": [],
                            "deprecationReason": null,
                            "description": "The value of this variable.",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "HostVariable",
                    "possibleTypes": null
                },
                {
                    "description": "A Directive can be adjacent to many parts of the GraphQL language, a __DirectiveLocation describes one such possible adjacencies.",
                    "enumValues": [
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a schema definition.",
                            "isDeprecated": false,
                            "name": "SCHEMA"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a scalar definition.",
                            "isDeprecated": false,
                            "name": "SCALAR"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to an interface definition.",
                            "isDeprecated": false,
                            "name": "INTERFACE"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a query operation.",
                            "isDeprecated": false,
                            "name": "QUERY"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a field.",
                            "isDeprecated": false,
                            "name": "FIELD"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to an inline fragment.",
                            "isDeprecated": false,
                            "name": "INLINE_FRAGMENT"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to an argument definition.",
                            "isDeprecated": false,
                            "name": "ARGUMENT_DEFINITION"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to an input object type definition.",
                            "isDeprecated": false,
                            "name": "INPUT_OBJECT"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a mutation operation.",
                            "isDeprecated": false,
                            "name": "MUTATION"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a subscription operation.",
                            "isDeprecated": false,
                            "name": "SUBSCRIPTION"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to an enum definition.",
                            "isDeprecated": false,
                            "name": "ENUM"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to an enum value definition.",
                            "isDeprecated": false,
                            "name": "ENUM_VALUE"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a field definition.",
                            "isDeprecated": false,
                            "name": "FIELD_DEFINITION"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a union definition.",
                            "isDeprecated": false,
                            "name": "UNION"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a object definition.",
                            "isDeprecated": false,
                            "name": "OBJECT"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to an input object field definition.",
                            "isDeprecated": false,
                            "name": "INPUT_FIELD_DEFINITION"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a fragment definition.",
                            "isDeprecated": false,
                            "name": "FRAGMENT_DEFINITION"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Location adjacent to a fragment spread.",
                            "isDeprecated": false,
                            "name": "FRAGMENT_SPREAD"
                        }
                    ],
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "ENUM",
                    "name": "__DirectiveLocation",
                    "possibleTypes": null
                },
                {
                    "description": "An enum describing what kind of type a given `__Type` is",
                    "enumValues": [
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is an enum. `enumValues` is a valid field.",
                            "isDeprecated": false,
                            "name": "ENUM"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is an input object. `inputFields` is a valid field.",
                            "isDeprecated": false,
                            "name": "INPUT_OBJECT"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is a list. `ofType` is a valid field.",
                            "isDeprecated": false,
                            "name": "LIST"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is a non-null. `ofType` is a valid field.",
                            "isDeprecated": false,
                            "name": "NON_NULL"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is a scalar.",
                            "isDeprecated": false,
                            "name": "SCALAR"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is an object. `fields` and `interfaces` are valid fields.",
                            "isDeprecated": false,
                            "name": "OBJECT"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is an interface. `fields` and `possibleTypes` are valid fields.",
                            "isDeprecated": false,
                            "name": "INTERFACE"
                        },
                        {
                            "deprecationReason": null,
                            "description": "Indicates this type is a union. `possibleTypes` is a valid field.",
                            "isDeprecated": false,
                            "name": "UNION"
                        }
                    ],
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "ENUM",
                    "name": "__TypeKind",
                    "possibleTypes": null
                },
                {
                    "description": "The `Int` scalar type represents non-fractional signed whole numeric values. Int can represent values between -(2^31) and 2^31 - 1. ",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "Int",
                    "possibleTypes": null
                },
                {
                    "description": "The platform config OS and architecture in a Container.\nThe format is [os]/[platform]/[version] (e.g. darwin/arm64/v7, windows/amd64, linux/arm64).",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "Platform",
                    "possibleTypes": null
                },
                {
                    "description": "A unique container identifier. Null designates an empty container (scratch).",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "ContainerID",
                    "possibleTypes": null
                },
                {
                    "description": "A directory whose contents persist across runs.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "",
                            "isDeprecated": false,
                            "name": "id",
                            "type": {
                                "kind": "NON_NULL",
                                "name": null,
                                "ofType": {
                                    "kind": "SCALAR",
                                    "name": "CacheID",
                                    "ofType": null
                                }
                            }
                        }
                    ],
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "CacheVolume",
                    "possibleTypes": null
                },
                {
                    "description": "A git ref (tag, branch or commit).",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "The digest of the current value of this ref.",
                            "isDeprecated": false,
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
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
                                    "name": "sshKnownHosts",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "GitRef",
                    "possibleTypes": null
                },
                {
                    "description": "The `String` scalar type represents textual data, represented as UTF-8 character sequences. The String type is most often used by GraphQL to represent free-form human-readable text.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "String",
                    "possibleTypes": null
                },
                {
                    "description": "The `Boolean` scalar type represents `true` or `false`.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "Boolean",
                    "possibleTypes": null
                },
                {
                    "description": "A simple key value object that represents a label.",
                    "enumValues": null,
                    "fields": [
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Label",
                    "possibleTypes": null
                },
                {
                    "description": "A GraphQL Schema defines the capabilities of a GraphQL server. It exposes all available types and directives on the server, as well as the entry points for query, mutation, and subscription operations.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "A list of all directives supported by this server.",
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
                            "description": "If this server supports mutation, the type that mutation operations will be rooted at.",
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
                            "description": "The type that query operations will be rooted at.",
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
                            "description": "If this server supports subscription, the type that subscription operations will be rooted at.",
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
                            "description": "A list of all types supported by this server.",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "__Schema",
                    "possibleTypes": null
                },
                {
                    "description": "",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "The content-addressed identifier of the socket.",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Socket",
                    "possibleTypes": null
                },
                {
                    "description": "The `DateTime` scalar type represents a DateTime. The DateTime is serialized as an RFC 3339 quoted string",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "DateTime",
                    "possibleTypes": null
                },
                {
                    "description": "",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "A string identifier to target this cache volume (e.g. \"myapp-cache\").",
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
                                    "description": "",
                                    "name": "id",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "ContainerID",
                                        "ofType": null
                                    }
                                },
                                {
                                    "defaultValue": null,
                                    "description": "",
                                    "name": "platform",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "Platform",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Loads a container from ID.\nNull ID returns an empty container (scratch).\nOptional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.",
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
                            "description": "The default platform of the builder.",
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
                                    "description": "",
                                    "name": "id",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "DirectoryID",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Load a directory by ID. No argument produces an empty directory.",
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
                            "deprecationReason": null,
                            "description": "Loads a file by ID.",
                            "isDeprecated": false,
                            "name": "file",
                            "type": {
                                "kind": "OBJECT",
                                "name": "File",
                                "ofType": null
                            }
                        },
                        {
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                                    "description": "",
                                    "name": "keepGitDir",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "Boolean",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Queries a git repository.",
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
                                    "description": "",
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
                                    "name": "description",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "String",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Creates a named sub-pipeline",
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
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Look up a project by name",
                            "isDeprecated": false,
                            "name": "project",
                            "type": {
                                "kind": "NON_NULL",
                                "name": null,
                                "ofType": {
                                    "kind": "OBJECT",
                                    "name": "Project",
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
                            "description": "Loads a secret from its ID.",
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
                                    "description": "",
                                    "name": "id",
                                    "type": {
                                        "kind": "SCALAR",
                                        "name": "SocketID",
                                        "ofType": null
                                    }
                                }
                            ],
                            "deprecationReason": null,
                            "description": "Loads a socket by its ID.",
                            "isDeprecated": false,
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
                        }
                    ],
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Query",
                    "possibleTypes": null
                },
                {
                    "description": "A file.",
                    "enumValues": null,
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
                                    "description": "",
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
                            "description": "Retrieves the content-addressed identifier of the file.",
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
                            "description": "Retrieves a secret referencing the contents of this file.",
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
                            "args": [],
                            "deprecationReason": null,
                            "description": "Gets the size of the file, in bytes.",
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
                            "args": [
                                {
                                    "defaultValue": null,
                                    "description": "",
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
                            "description": "Retrieves this file with its created/modified timestamps set to the given time, in seconds from the Unix epoch.",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "File",
                    "possibleTypes": null
                },
                {
                    "description": "A reference to a secret value, which can be handled more safely than the value itself.",
                    "enumValues": null,
                    "fields": [
                        {
                            "args": [],
                            "deprecationReason": null,
                            "description": "The identifier for this secret.",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "Secret",
                    "possibleTypes": null
                },
                {
                    "description": "",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": [
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
                    "interfaces": null,
                    "kind": "INPUT_OBJECT",
                    "name": "BuildArg",
                    "possibleTypes": null
                },
                {
                    "description": "A content-addressed socket identifier.",
                    "enumValues": null,
                    "fields": null,
                    "inputFields": null,
                    "interfaces": null,
                    "kind": "SCALAR",
                    "name": "SocketID",
                    "possibleTypes": null
                },
                {
                    "description": "A Directive provides a way to describe alternate runtime execution and type validation behavior in a GraphQL document. \n\nIn some cases, you need to provide options to alter GraphQL's execution behavior in ways field arguments will not suffice, such as conditionally including or skipping a field. Directives provide this by describing additional information to the executor.",
                    "enumValues": null,
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
                        },
                        {
                            "args": [],
                            "deprecationReason": "Use `locations`.",
                            "description": "",
                            "isDeprecated": true,
                            "name": "onField",
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
                            "deprecationReason": "Use `locations`.",
                            "description": "",
                            "isDeprecated": true,
                            "name": "onFragment",
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
                            "deprecationReason": "Use `locations`.",
                            "description": "",
                            "isDeprecated": true,
                            "name": "onOperation",
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "__Directive",
                    "possibleTypes": null
                },
                {
                    "description": "One possible value for a given Enum. Enum values are unique values, not a placeholder for a string or numeric value. However an Enum value is returned in a JSON response as a string.",
                    "enumValues": null,
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
                    "inputFields": null,
                    "interfaces": [],
                    "kind": "OBJECT",
                    "name": "__EnumValue",
                    "possibleTypes": null
                }
            ]
        }
    }
}"#;
}
