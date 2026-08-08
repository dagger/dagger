use dagger_sdk::{Container, Directory, Query};

fn incompatible(query: &Query, container: Container) {
    let _: Directory = query.r#ref(container);
}

fn main() {}
