# Dagger Rust SDK Examples

## [CLI](./cli/src/main.rs)

This CI pipeline is an example of how to use the Dagger SDK to automate the build of a Rust CLI application.

Therefore source code from the host is mounted into a build container, where cargo build --release is executed to compile the application.

The resulting binary is then exported to the host machine.


## [Backend](./backend/src/main.rs)

This CI pipeline is an example of how to use the Dagger SDK to automate the build of a backend service using **Axum** framework.

The pipeline demonstrates how to split a more complex pipeline into different functions and contains the following steps:

1. **Building the Backend**
2. **Creating the Production Image** 
3. **Publishing the Image**

**Clap** is used to configure the build.

## [Frontend](./frontend/src/main.rs)

This CI pipeline automates the build of a Rust-based frontend application based on **Leptos** & **Tailwind**.

Similar to the backend pipeline, the pipeline is subdivided into smaller steps:

1. **Building the Frontend**
2. **Creating the Production Image**
3. **Publishing the Image**
