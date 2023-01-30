pub mod introspection;

pub struct Scalar(String);

pub struct Boolean(bool);

pub struct Int(isize);

pub trait Input {}
