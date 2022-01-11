# Change Log

All notable changes to this project will be documented in this file.
This project adheres to [Semantic Versioning](http://semver.org/).


## [0.3.0] - 2016-03-22

### Removed

- Move `fail()` to `bats-support`


## [0.2.0] - 2016-03-11

### Added

- `refute()` to complement `assert()`
- `npm` support

### Fixed

- Not consuming the `--` when stopping option parsing in
  `assert_output`, `refute_output`, `assert_line` and `refute_line`


## 0.1.0 - 2016-02-16

### Added

- Reporting arbitrary failures with `fail()`
- Generic assertions with `assert()` and `assert_equal()`
- Testing exit status with `assert_success()` and `assert_failure()`
- Testing output with `assert_output()` and `refute_output()`
- Testing individual lines with `assert_line()` and `refute_line()`


[0.3.0]: https://github.com/ztombol/bats-assert/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ztombol/bats-assert/compare/v0.1.0...v0.2.0
