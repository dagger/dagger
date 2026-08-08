use dagger_sdk::{Container, Directory, Id, IdInput, NodeClient, Query};

fn accepted(container: Container, directory: Directory, id: Id) {
    let _: IdInput<Container> = container.clone().into();
    let _: IdInput<NodeClient> = container.into();
    let _: IdInput<Directory> = directory.into();
    let _: IdInput<Container> = id.into();
}

fn query_entrypoints(query: &Query, container: Container, id: Id) {
    let _: Container = query.r#ref(container);
    let _: Container = query.r#ref(id.clone());
    let _ = query.load::<Container>(id);
}

fn main() {}
