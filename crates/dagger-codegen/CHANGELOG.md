# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.2.8 (2023-02-22)

### New Features

 - <csr-id-266ad32dff4c8957c7cdd291f9ef6f8a8c1d055c/> with clone

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 1 commit contributed to the release.
 - 2 days passed between releases.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - with clone ([`266ad32`](https://github.com/kjuulh/dagger-rs/commit/266ad32dff4c8957c7cdd291f9ef6f8a8c1d055c))
</details>

## v0.2.7 (2023-02-20)

### Bug Fixes

 - <csr-id-a13a2a9ecbfdfac80ed8eb0cbb9e9db317da65de/> race condition in process

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 3 commits contributed to the release.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-core v0.2.6, dagger-codegen v0.2.7, dagger-sdk v0.2.12 ([`7179f8b`](https://github.com/kjuulh/dagger-rs/commit/7179f8b598ef04e62925e39d3f55740253c01686))
    - Release dagger-core v0.2.5, dagger-sdk v0.2.12, dagger-codegen v0.2.7 ([`1725c51`](https://github.com/kjuulh/dagger-rs/commit/1725c5188e8a81069ec4a4de569484c921a94927))
    - race condition in process ([`a13a2a9`](https://github.com/kjuulh/dagger-rs/commit/a13a2a9ecbfdfac80ed8eb0cbb9e9db317da65de))
</details>

## v0.2.6 (2023-02-20)

<csr-id-803cfc4f8c4d72ab7d011be5523b3bfc6039de39/>

### Chore

 - <csr-id-803cfc4f8c4d72ab7d011be5523b3bfc6039de39/> ran clippy

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 3 commits contributed to the release.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-core v0.2.4, dagger-codegen v0.2.6, dagger-sdk v0.2.11 ([`f869e57`](https://github.com/kjuulh/dagger-rs/commit/f869e574dd788cd60e5b1b5d502bec68e300694c))
    - Release dagger-core v0.2.4, dagger-codegen v0.2.6, dagger-sdk v0.2.11 ([`17ec62a`](https://github.com/kjuulh/dagger-rs/commit/17ec62a5d58232ff57391523b9851fb7b07d02ab))
    - ran clippy ([`803cfc4`](https://github.com/kjuulh/dagger-rs/commit/803cfc4f8c4d72ab7d011be5523b3bfc6039de39))
</details>

## v0.2.5 (2023-02-19)

### New Features

 - <csr-id-978ede68ae52f5b5150a2aa45b8d6e1fbbbee2f4/> add documentation strings
 - <csr-id-9be6f435d9ea39f31a8906e55dbd3e8b1e5ec598/> Use async runtime instead of blocking.
   Default to using async runtime instead of blocking. I.e.
   
   ```rust
   fn main() -> eyre::Result<()> {
   // ...
   
   client.container().from("rust").publish("somewhere")?;
   
   // ...
   }
   
   // to
   
   async fn main() -> eyre::Result<()> {
   // ...
   
   client.container().from("rust").publish("somewhere").await?;
   
   // ...
   }
   ```

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 3 commits contributed to the release.
 - 2 commits were understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-sdk v0.2.8, dagger-codegen v0.2.5 ([`0499024`](https://github.com/kjuulh/dagger-rs/commit/04990247ba8e9d0555847f582fef14849dbedebf))
    - add documentation strings ([`978ede6`](https://github.com/kjuulh/dagger-rs/commit/978ede68ae52f5b5150a2aa45b8d6e1fbbbee2f4))
    - Use async runtime instead of blocking. ([`9be6f43`](https://github.com/kjuulh/dagger-rs/commit/9be6f435d9ea39f31a8906e55dbd3e8b1e5ec598))
</details>

## v0.2.4 (2023-02-19)

### New Features

 - <csr-id-f29ff836cfd72d5e051ca6a71a230ba1e9933091/> without Some in _opts functions
   Option has been removed as a wrapper around opts. This makes it much
   more convenient to use
   
   ```rust
   client.container_opts(Some(ContainerOpts{}))
   // ->
   client.container_opts(ContainerOpts{})
   ```
   
   The same options are still available, either an empty object can be
   passed, or a non _opts function can be used
 - <csr-id-9762da895a164e30c5dc60e89a83e934ceae47ab/> with _opts methods
   Now all opt values enter into a _opts function instead of the original.
   This avoids a lot of verbosity for both None in the case opts are
   unwanted, and Some() if they actually are.
   
   They are used like so:
   
   ```rust
   client.container().from("...");
   client.container_opts(Some(ContainerOpts{ ... }))
   ```
   
   Some from opts will be removed in a future commit/pr
 - <csr-id-94336d06378f035464e233b921dc3858070f582d/> move to &str instead of String and introduce builder.
   This will make the api much easier to use, as we can now rely on ""
   instead of "".into() for normal string values.
   
   Introduced builder as well, which makes it much easier to use *Opts, as
   it can handle the building of that, and get the benefits from String ->
   &str, as that is currently not allowed for optional values

### Bug Fixes

 - <csr-id-c627595fd2695e236924175d137c42f1480ccd6b/> cargo clippy
 - <csr-id-02006d40fc2c0383e0412c15c36db9af7eda991f/> without phantom data

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 6 commits contributed to the release.
 - 5 commits were understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-sdk v0.2.5, dagger-codegen v0.2.4 ([`f727318`](https://github.com/kjuulh/dagger-rs/commit/f72731807d8358fdb3d80432136b7a08bb7b1773))
    - cargo clippy ([`c627595`](https://github.com/kjuulh/dagger-rs/commit/c627595fd2695e236924175d137c42f1480ccd6b))
    - without Some in _opts functions ([`f29ff83`](https://github.com/kjuulh/dagger-rs/commit/f29ff836cfd72d5e051ca6a71a230ba1e9933091))
    - with _opts methods ([`9762da8`](https://github.com/kjuulh/dagger-rs/commit/9762da895a164e30c5dc60e89a83e934ceae47ab))
    - without phantom data ([`02006d4`](https://github.com/kjuulh/dagger-rs/commit/02006d40fc2c0383e0412c15c36db9af7eda991f))
    - move to &str instead of String and introduce builder. ([`94336d0`](https://github.com/kjuulh/dagger-rs/commit/94336d06378f035464e233b921dc3858070f582d))
</details>

## v0.2.3 (2023-02-19)

### New Features

 - <csr-id-de063eae858eb3335d2558a57ee6a88689635200/> with return result instead of unwrap
 - <csr-id-5d667369900a47d3a6015cd3814c240bc5c54436/> remove unnecessary option returns

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 3 commits contributed to the release.
 - 2 commits were understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-sdk v0.2.3, dagger-codegen v0.2.3, dagger-rs v0.2.9 ([`9235030`](https://github.com/kjuulh/dagger-rs/commit/92350306b3f0da40b4fc6dcaffcd90b891e83f70))
    - with return result instead of unwrap ([`de063ea`](https://github.com/kjuulh/dagger-rs/commit/de063eae858eb3335d2558a57ee6a88689635200))
    - remove unnecessary option returns ([`5d66736`](https://github.com/kjuulh/dagger-rs/commit/5d667369900a47d3a6015cd3814c240bc5c54436))
</details>

## v0.2.2 (2023-02-19)

### New Features

 - <csr-id-6e5f4074329ab0462445b31d4153f8497c483438/> update to dagger v0.3.12

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 2 commits contributed to the release.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-core v0.2.2, dagger-codegen v0.2.2, dagger-rs v0.2.8 ([`1638f15`](https://github.com/kjuulh/dagger-rs/commit/1638f15fba9d16512e8452f87b908d6dce417cd9))
    - update to dagger v0.3.12 ([`6e5f407`](https://github.com/kjuulh/dagger-rs/commit/6e5f4074329ab0462445b31d4153f8497c483438))
</details>

## v0.2.1 (2023-02-18)

<csr-id-6afe141d34308f18f9d46419931d2c9b822a7aef/>

### Bug Fixes

 - <csr-id-789b0e69c8c53d0e86d9cec89ab5345507aad514/> update all dependencies

### Other

 - <csr-id-6afe141d34308f18f9d46419931d2c9b822a7aef/> fix

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 35 commits contributed to the release over the course of 20 calendar days.
 - 2 commits were understood as [conventional](https://www.conventionalcommits.org).
 - 2 unique issues were worked on: [#5](https://github.com/kjuulh/dagger-rs/issues/5), [#6](https://github.com/kjuulh/dagger-rs/issues/6)

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **[#5](https://github.com/kjuulh/dagger-rs/issues/5)**
    - update all dependencies ([`789b0e6`](https://github.com/kjuulh/dagger-rs/commit/789b0e69c8c53d0e86d9cec89ab5345507aad514))
 * **[#6](https://github.com/kjuulh/dagger-rs/issues/6)**
    - feature/add impl ([`4a4c03f`](https://github.com/kjuulh/dagger-rs/commit/4a4c03f3c2ee7f6268c65976715e70767b4ea78d))
 * **Uncategorized**
    - Release dagger-core v0.2.1, dagger-codegen v0.2.1, dagger-rs v0.2.1 ([`1332bc8`](https://github.com/kjuulh/dagger-rs/commit/1332bc842ce2ea0254c651419813b63b36ca590c))
    - add changelogs ([`a064684`](https://github.com/kjuulh/dagger-rs/commit/a064684fcf80196188a57d9ff9067c0b5769fb09))
    - Adjusting changelogs prior to release of dagger-core v0.2.1, dagger-codegen v0.2.1, dagger-rs v0.2.1 ([`f4a20fd`](https://github.com/kjuulh/dagger-rs/commit/f4a20fda79063b29829cc899793775ba8cb17214))
    - remove toolchain ([`f034528`](https://github.com/kjuulh/dagger-rs/commit/f03452840cf9260cd1d5e5aa8d7ee2897384c745))
    - with actual versions ([`7153c24`](https://github.com/kjuulh/dagger-rs/commit/7153c24f0105a05f170efd10ef2535d83ce0c87e))
    - with publish ([`989d5bc`](https://github.com/kjuulh/dagger-rs/commit/989d5bc26036d46a199d939b5cbbe72aff2f8fb1))
    - codegen also with repository ([`c625ae4`](https://github.com/kjuulh/dagger-rs/commit/c625ae49ba4d2112ea9d4907a6689fd8e74b808c))
    - with readme and license ([`1e26b38`](https://github.com/kjuulh/dagger-rs/commit/1e26b383d4f6dbcbe20f5f7c19c749e743f6e607))
    - with wildcard version ([`533b9df`](https://github.com/kjuulh/dagger-rs/commit/533b9dfef0165c514127a8437d08daf52adf5739))
    - cargo version 0.2.0 ([`bec62de`](https://github.com/kjuulh/dagger-rs/commit/bec62de62ff5638428174e232a36eee3ddd0f5ef))
    - bump version ([`36b0ecd`](https://github.com/kjuulh/dagger-rs/commit/36b0ecdabf4c220cffb2d0660fb6480387e3249a))
    - fix all clippy ([`6be8482`](https://github.com/kjuulh/dagger-rs/commit/6be8482b461e098384bbf1371ed7d67b259197fa))
    - fmt tests ([`2eb0277`](https://github.com/kjuulh/dagger-rs/commit/2eb027754b357100544fe0c8f7c5f6125e017c6f))
    - add tests ([`19b46b6`](https://github.com/kjuulh/dagger-rs/commit/19b46b6cf04ff3cff49047699dea20ca784c5536))
    - pull out args wip ([`c4edd29`](https://github.com/kjuulh/dagger-rs/commit/c4edd29f50b6ada2cc3afd2f4df2ec47920c4607))
    - implement sort by name and type ([`d9b51c1`](https://github.com/kjuulh/dagger-rs/commit/d9b51c1ac90c00fb3af24332b6140e1201bc9be7))
    - fix optional types for real ([`26069a8`](https://github.com/kjuulh/dagger-rs/commit/26069a82a69ec7265216c8ddaceb37228dd0fb81))
    - fix description ([`f4581ba`](https://github.com/kjuulh/dagger-rs/commit/f4581ba4cd1693a906eaf6c58054398ceae3bfac))
    - with proper optional types ([`f4a812a`](https://github.com/kjuulh/dagger-rs/commit/f4a812a7d24e9e09cb4e3cbde56ee0b3ac774b62))
    - set proper option type ([`8549cfc`](https://github.com/kjuulh/dagger-rs/commit/8549cfc3a7d9f831febaeadc22db36604e465ea8))
    - add fields ([`496a687`](https://github.com/kjuulh/dagger-rs/commit/496a687bc34f7c58cc86df60c183be741b0b8a9c))
    - add input_fields ([`d2cddff`](https://github.com/kjuulh/dagger-rs/commit/d2cddff365c636feceb3f20a73df812fcab11a19))
    - with objects ([`5fef514`](https://github.com/kjuulh/dagger-rs/commit/5fef5148010f384d0158361d64b8e17d357d4819))
    - remove hardcoded test ([`910ff4a`](https://github.com/kjuulh/dagger-rs/commit/910ff4a72e10f5384287fed35f56bc7f662e7ccd))
    - fix ([`6afe141`](https://github.com/kjuulh/dagger-rs/commit/6afe141d34308f18f9d46419931d2c9b822a7aef))
    - formatting ([`3a7ee33`](https://github.com/kjuulh/dagger-rs/commit/3a7ee33e1ed317288b2022ea5a4ce721d59fb11e))
    - remove dummy string ([`e7f6560`](https://github.com/kjuulh/dagger-rs/commit/e7f6560247768afbca0c350df7d4ccf3909b74fa))
    - with input objects ([`dc53fc1`](https://github.com/kjuulh/dagger-rs/commit/dc53fc1d474b549bb1c580865a049e2fac2f5e6d))
    - with enum ([`2a1f7c3`](https://github.com/kjuulh/dagger-rs/commit/2a1f7c3f2666f1f4caebf7c22707709741c2cfad))
    - with codegen output ([`0bf6b0e`](https://github.com/kjuulh/dagger-rs/commit/0bf6b0e91ecc31c1f6b51338234137eb185810a0))
    - added scalars ([`e587414`](https://github.com/kjuulh/dagger-rs/commit/e5874141b3b6256b7ac2a0bf653089fa7bcc5d14))
    - with scalars ([`0d6e6e5`](https://github.com/kjuulh/dagger-rs/commit/0d6e6e57ae6a3b8a1f450d719c9973130af873b7))
    - split out codegen parts ([`3263f1d`](https://github.com/kjuulh/dagger-rs/commit/3263f1d589aee78065401c666533cb0cbadd06ce))
</details>

