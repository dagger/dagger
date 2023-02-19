# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.2.8 (2023-02-19)

### New Features

 - <csr-id-978ede68ae52f5b5150a2aa45b8d6e1fbbbee2f4/> add documentation strings

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 1 commit contributed to the release.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - add documentation strings ([`978ede6`](https://github.com/kjuulh/dagger-rs/commit/978ede68ae52f5b5150a2aa45b8d6e1fbbbee2f4))
</details>

## v0.2.7 (2023-02-19)

### Documentation

 - <csr-id-93f40b356c48f14e910968516bed9487912095c1/> change to await syntax

### New Features

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
    - Release dagger-sdk v0.2.7 ([`a1887af`](https://github.com/kjuulh/dagger-rs/commit/a1887afc8b51f61491ba7f13c5e7a5b7619623c4))
    - change to await syntax ([`93f40b3`](https://github.com/kjuulh/dagger-rs/commit/93f40b356c48f14e910968516bed9487912095c1))
    - Use async runtime instead of blocking. ([`9be6f43`](https://github.com/kjuulh/dagger-rs/commit/9be6f435d9ea39f31a8906e55dbd3e8b1e5ec598))
</details>

## v0.2.6 (2023-02-19)

### Documentation

 - <csr-id-04e70ce964b343e28b3dbd0c46d10ccda958ab8c/> fix readme

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 2 commits contributed to the release.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-sdk v0.2.6 ([`c312bc5`](https://github.com/kjuulh/dagger-rs/commit/c312bc57ad3e5380b6a2a927f3bb758aa5344efd))
    - fix readme ([`04e70ce`](https://github.com/kjuulh/dagger-rs/commit/04e70ce964b343e28b3dbd0c46d10ccda958ab8c))
</details>

## v0.2.5 (2023-02-19)

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
 - <csr-id-6e2292cf11942fbd26a52fe4e0fc8471e6eb70a3/> dependencies

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 7 commits contributed to the release.
 - 6 commits were understood as [conventional](https://www.conventionalcommits.org).
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
    - dependencies ([`6e2292c`](https://github.com/kjuulh/dagger-rs/commit/6e2292cf11942fbd26a52fe4e0fc8471e6eb70a3))
</details>

## v0.2.4 (2023-02-19)

### Bug Fixes

 - <csr-id-7d04ab1240e497e7804fed23a378d28c78f50a0a/> readme dagger-rs -> dagger-sdk

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 2 commits contributed to the release.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-sdk v0.2.4 ([`cc81124`](https://github.com/kjuulh/dagger-rs/commit/cc81124f899f44f80c1ee7d1e23a7e02d8cc4b7c))
    - readme dagger-rs -> dagger-sdk ([`7d04ab1`](https://github.com/kjuulh/dagger-rs/commit/7d04ab1240e497e7804fed23a378d28c78f50a0a))
</details>

## v0.2.3 (2023-02-19)

### New Features

 - <csr-id-19ed6c267f779b72430422c463ceed553f6fc618/> re-export through lib.rs
   this means that you can now use dagger_sdk::connect() instead of
   dagger_sdk::client::connect();
 - <csr-id-de063eae858eb3335d2558a57ee6a88689635200/> with return result instead of unwrap
 - <csr-id-5d667369900a47d3a6015cd3814c240bc5c54436/> remove unnecessary option returns

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 4 commits contributed to the release.
 - 3 commits were understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-sdk v0.2.3, dagger-codegen v0.2.3, dagger-rs v0.2.9 ([`9235030`](https://github.com/kjuulh/dagger-rs/commit/92350306b3f0da40b4fc6dcaffcd90b891e83f70))
    - re-export through lib.rs ([`19ed6c2`](https://github.com/kjuulh/dagger-rs/commit/19ed6c267f779b72430422c463ceed553f6fc618))
    - with return result instead of unwrap ([`de063ea`](https://github.com/kjuulh/dagger-rs/commit/de063eae858eb3335d2558a57ee6a88689635200))
    - remove unnecessary option returns ([`5d66736`](https://github.com/kjuulh/dagger-rs/commit/5d667369900a47d3a6015cd3814c240bc5c54436))
</details>

## v0.2.2 (2023-02-19)

### New Features

 - <csr-id-6e5f4074329ab0462445b31d4153f8497c483438/> update to dagger v0.3.12

### Bug Fixes

 - <csr-id-10bc6f3846b65cc82c2fb343d8cfe921784bef1b/> fixed fmt errors

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 4 commits contributed to the release.
 - 2 commits were understood as [conventional](https://www.conventionalcommits.org).
 - 0 issues like '(#ID)' were seen in commit messages

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **Uncategorized**
    - Release dagger-sdk v0.2.2 ([`e921ba1`](https://github.com/kjuulh/dagger-rs/commit/e921ba13638987ccf5beaa48c4be9be5fd879bd0))
    - Release dagger-core v0.2.2, dagger-codegen v0.2.2, dagger-rs v0.2.8 ([`1638f15`](https://github.com/kjuulh/dagger-rs/commit/1638f15fba9d16512e8452f87b908d6dce417cd9))
    - fixed fmt errors ([`10bc6f3`](https://github.com/kjuulh/dagger-rs/commit/10bc6f3846b65cc82c2fb343d8cfe921784bef1b))
    - update to dagger v0.3.12 ([`6e5f407`](https://github.com/kjuulh/dagger-rs/commit/6e5f4074329ab0462445b31d4153f8497c483438))
</details>

## v0.2.1 (2023-02-18)

### Bug Fixes

 - <csr-id-789b0e69c8c53d0e86d9cec89ab5345507aad514/> update all dependencies

### Commit Statistics

<csr-read-only-do-not-edit/>

 - 40 commits contributed to the release over the course of 20 calendar days.
 - 1 commit was understood as [conventional](https://www.conventionalcommits.org).
 - 2 unique issues were worked on: [#5](https://github.com/kjuulh/dagger-rs/issues/5), [#6](https://github.com/kjuulh/dagger-rs/issues/6)

### Commit Details

<csr-read-only-do-not-edit/>

<details><summary>view details</summary>

 * **[#5](https://github.com/kjuulh/dagger-rs/issues/5)**
    - update all dependencies ([`789b0e6`](https://github.com/kjuulh/dagger-rs/commit/789b0e69c8c53d0e86d9cec89ab5345507aad514))
 * **[#6](https://github.com/kjuulh/dagger-rs/issues/6)**
    - feature/add impl ([`4a4c03f`](https://github.com/kjuulh/dagger-rs/commit/4a4c03f3c2ee7f6268c65976715e70767b4ea78d))
 * **Uncategorized**
    - Release dagger-sdk v0.2.1 ([`aa0c397`](https://github.com/kjuulh/dagger-rs/commit/aa0c397b15566840eb59ca6186c083f631cc460b))
    - add dagger-sdk changelog ([`11a5247`](https://github.com/kjuulh/dagger-rs/commit/11a5247933736bc6a4c5300c29599c88597fefb7))
    - Release dagger-rs v0.2.7, dagger-sdk v0.2.1 ([`20c7118`](https://github.com/kjuulh/dagger-rs/commit/20c71189f6d5d978286ee16f8e958c6045756d80))
    - Adjusting changelogs prior to release of dagger-core v0.2.1, dagger-codegen v0.2.1, dagger-rs v0.2.1 ([`f4a20fd`](https://github.com/kjuulh/dagger-rs/commit/f4a20fda79063b29829cc899793775ba8cb17214))
    - with actual versions ([`7153c24`](https://github.com/kjuulh/dagger-rs/commit/7153c24f0105a05f170efd10ef2535d83ce0c87e))
    - with publish ([`989d5bc`](https://github.com/kjuulh/dagger-rs/commit/989d5bc26036d46a199d939b5cbbe72aff2f8fb1))
    - with repo ([`e5383b5`](https://github.com/kjuulh/dagger-rs/commit/e5383b51f3b290a87b729929c377e93bda3873e0))
    - with wildcard version ([`533b9df`](https://github.com/kjuulh/dagger-rs/commit/533b9dfef0165c514127a8437d08daf52adf5739))
    - cargo version 0.2.0 ([`bec62de`](https://github.com/kjuulh/dagger-rs/commit/bec62de62ff5638428174e232a36eee3ddd0f5ef))
    - bump version ([`36b0ecd`](https://github.com/kjuulh/dagger-rs/commit/36b0ecdabf4c220cffb2d0660fb6480387e3249a))
    - document usage ([`578c2a6`](https://github.com/kjuulh/dagger-rs/commit/578c2a68830eb40da888823a8770af4a764ed4c7))
    - fix all clippy ([`6be8482`](https://github.com/kjuulh/dagger-rs/commit/6be8482b461e098384bbf1371ed7d67b259197fa))
    - add with dockerfile ([`0cbd179`](https://github.com/kjuulh/dagger-rs/commit/0cbd1790b0b4030c68f0a0dd619325da26f14f60))
    - with caching ([`728840c`](https://github.com/kjuulh/dagger-rs/commit/728840ca8e48b8bec66da4e5fa677bfa60d1d147))
    - add more quickstart ([`59e2572`](https://github.com/kjuulh/dagger-rs/commit/59e2572173872c8091a0613a387a01e0cccc51bf))
    - build the application ([`d894def`](https://github.com/kjuulh/dagger-rs/commit/d894def70c85ff2fc567bf614e3be6f4134965e2))
    - add test-the-application ([`cb9a4dd`](https://github.com/kjuulh/dagger-rs/commit/cb9a4dd84fc13ef03ca3ad539646e95c3c047676))
    - with println ([`d1726a0`](https://github.com/kjuulh/dagger-rs/commit/d1726a052a6dc4e57f364864446cab3cbda7e0bf))
    - unpack response ([`3b5b59b`](https://github.com/kjuulh/dagger-rs/commit/3b5b59ba1c20cc68218dc5c0af18ff7a78f6953d))
    - tested full flow initially ([`7a008be`](https://github.com/kjuulh/dagger-rs/commit/7a008be59e5ca183809e5840cdfae1d87665aa20))
    - move code to dagger-core ([`ec0d0b2`](https://github.com/kjuulh/dagger-rs/commit/ec0d0b22e646c97acb3ce93f3afb3ddb8590e68f))
    - with selection impl default ([`9f0021b`](https://github.com/kjuulh/dagger-rs/commit/9f0021b7086046c80b3f455f205149e03eb72da2))
    - fix warnings ([`2b49f9c`](https://github.com/kjuulh/dagger-rs/commit/2b49f9c19098d96df2bb735253710774b0831c94))
    - fix test ([`03366b7`](https://github.com/kjuulh/dagger-rs/commit/03366b7c5b3cce5ec42b5c7655843170236c56a1))
    - test marshaller ([`c5dfceb`](https://github.com/kjuulh/dagger-rs/commit/c5dfcebaad9c255b10ba8c6e4d4dba00821c8941))
    - test marshaller ([`c4ec6f0`](https://github.com/kjuulh/dagger-rs/commit/c4ec6f0c976ce0af2e05e818731b5e2bed7f0522))
    - implement sort by name and type ([`d9b51c1`](https://github.com/kjuulh/dagger-rs/commit/d9b51c1ac90c00fb3af24332b6140e1201bc9be7))
    - fix optional types for real ([`26069a8`](https://github.com/kjuulh/dagger-rs/commit/26069a82a69ec7265216c8ddaceb37228dd0fb81))
    - fix description ([`f4581ba`](https://github.com/kjuulh/dagger-rs/commit/f4581ba4cd1693a906eaf6c58054398ceae3bfac))
    - with proper optional types ([`f4a812a`](https://github.com/kjuulh/dagger-rs/commit/f4a812a7d24e9e09cb4e3cbde56ee0b3ac774b62))
    - set proper option type ([`8549cfc`](https://github.com/kjuulh/dagger-rs/commit/8549cfc3a7d9f831febaeadc22db36604e465ea8))
    - add fields ([`496a687`](https://github.com/kjuulh/dagger-rs/commit/496a687bc34f7c58cc86df60c183be741b0b8a9c))
    - add input_fields ([`d2cddff`](https://github.com/kjuulh/dagger-rs/commit/d2cddff365c636feceb3f20a73df812fcab11a19))
    - with objects ([`5fef514`](https://github.com/kjuulh/dagger-rs/commit/5fef5148010f384d0158361d64b8e17d357d4819))
    - with enum ([`2a1f7c3`](https://github.com/kjuulh/dagger-rs/commit/2a1f7c3f2666f1f4caebf7c22707709741c2cfad))
    - with codegen output ([`0bf6b0e`](https://github.com/kjuulh/dagger-rs/commit/0bf6b0e91ecc31c1f6b51338234137eb185810a0))
    - split out codegen parts ([`3263f1d`](https://github.com/kjuulh/dagger-rs/commit/3263f1d589aee78065401c666533cb0cbadd06ce))
    - add dagger-sdk ([`9dccb83`](https://github.com/kjuulh/dagger-rs/commit/9dccb83d94a720dd58deffe9f3e5aaea784336f3))
</details>

