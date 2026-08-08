struct Query;

impl Query {
    fn load(&self, address: String) {
        drop(address);
    }
}

fn main() {
    Query.load();
}
