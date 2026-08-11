//! Deterministic const-generic identities for the shared authoring grammar.

const FNV_OFFSET_BASIS: u128 = 0x6c62_272e_07bb_0142_62b8_2175_6295_c58d;
const FNV_PRIME: u128 = 0x0000_0000_0100_0000_0000_0000_0000_013b;

pub(crate) fn fingerprint(parts: impl IntoIterator<Item = String>) -> u128 {
    let mut value = FNV_OFFSET_BASIS;
    for part in parts {
        let length = u64::try_from(part.len()).unwrap_or(u64::MAX);
        for byte in length.to_le_bytes().into_iter().chain(part.bytes()) {
            value ^= u128::from(byte);
            value = value.wrapping_mul(FNV_PRIME);
        }
    }
    value
}

#[cfg(test)]
mod tests {
    use super::fingerprint;

    #[test]
    fn framing_distinguishes_otherwise_ambiguous_parts() {
        assert_ne!(
            fingerprint(["ab".to_owned(), "c".to_owned()]),
            fingerprint(["a".to_owned(), "bc".to_owned()])
        );
    }
}
