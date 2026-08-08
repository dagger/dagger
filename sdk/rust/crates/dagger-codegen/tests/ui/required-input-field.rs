struct Input {
    required: String,
}

impl Input {
    fn new(required: String) -> Self {
        Self { required }
    }
}

fn main() {
    let _ = Input::new();
}
