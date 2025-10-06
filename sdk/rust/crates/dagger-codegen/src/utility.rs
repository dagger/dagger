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
        (*self).as_ref().map(f)
    }
}
