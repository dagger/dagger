use dagger_sdk::{Container, Node, NodeClient};

fn require_node<T: Node>() {}

fn exercise_node<T: Node>(node: &T) {
    let _ = node.id();
}

fn main() {
    require_node::<Container>();
    require_node::<NodeClient>();
    let _ = exercise_node::<Container>;
    let _ = exercise_node::<NodeClient>;
}
