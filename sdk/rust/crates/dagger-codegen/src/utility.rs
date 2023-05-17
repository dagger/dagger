pub trait OptionExt<'t, T: 't> {
    fn pipe<U, F>(&'t self, f: F) -> Option<U>
    where
        F: FnOnce(&'t T) -> U;
}

impl<'t, T: 't> OptionExt<'t, T> for Option<T> {
    #[inline]
    fn pipe<U, F>(&'t self, f: F) -> Option<U>
    where
        F: FnOnce(&'t T) -> U,
    {
        match *self {
            Some(ref x) => Some(f(x)),
            None => None,
        }
    }
}
