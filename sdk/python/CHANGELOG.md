## v0.2.0

- 🐍 https://pypi.org/project/dagger-io/0.2.0
- 📖 https://dagger-io.readthedocs.io/en/sdk-python-v0.2.0/

### Breaking Changes
* sdk: python: Pass objects instead of IDs by @helderco in https://github.com/dagger/dagger/pull/3870
* replace FS by RootFS by @aluzzardi in https://github.com/dagger/dagger/pull/3882
* api: Deprecate host.workdir by @aluzzardi in https://github.com/dagger/dagger/pull/3910
* api: directory.withNewFile: make `contents` mandatory by @aluzzardi in https://github.com/dagger/dagger/pull/3911
* api: Return the contents directly in Container Stdout/Stderr by @aluzzardi in https://github.com/dagger/dagger/pull/3925
* api: Deprecate Exec in favor of WithExec. Make args mandatory. by @aluzzardi in https://github.com/dagger/dagger/pull/3928

### Other Changes
* docs: remove --pre from pip install by @jpadams in https://github.com/dagger/dagger/pull/3778
* ci: automate SDK engine bumps by @aluzzardi in https://github.com/dagger/dagger/pull/3786
* Add docker image provisioner test. by @sipsma in https://github.com/dagger/dagger/pull/3763
* sdk: python: Update project links by @helderco in https://github.com/dagger/dagger/pull/3843
* sdk: python: Fix linter on poe task by @helderco in https://github.com/dagger/dagger/pull/3844
* sdk: python: Chain exceptions properly by @helderco in https://github.com/dagger/dagger/pull/3845
* sdk: python: Avoid shadowing built-ins by @helderco in https://github.com/dagger/dagger/pull/3846
* sdk: python: Fix README.md example by @helderco in https://github.com/dagger/dagger/pull/3847
* sdk: python: Fix windows platform detection by @helderco in https://github.com/dagger/dagger/pull/3814
* chore(pyhon): Add dependabot by @helderco in https://github.com/dagger/dagger/pull/3815
* Test Python 3.11 by @helderco in https://github.com/dagger/dagger/pull/3816
* Python: Improve error messages by @KGB33 in https://github.com/dagger/dagger/pull/3825
* docs(python): port private registry docs to python by @grouville in https://github.com/dagger/dagger/pull/3760
* Handle etxtbsy error in python sdk. by @sipsma in https://github.com/dagger/dagger/pull/3905
* Support full isolated sessions w/ dagger-in-dagger.  by @sipsma in https://github.com/dagger/dagger/pull/3787
* sdk: python: Fix .gitattributes by @helderco in https://github.com/dagger/dagger/pull/3926
* ci: cli distribution by @marcosnils in https://github.com/dagger/dagger/pull/3901
* docs(python): Lint doc snippets by @helderco in https://github.com/dagger/dagger/pull/3937
* sdk: python: Allow passing objects in params named `id` by @helderco in https://github.com/dagger/dagger/pull/3936
* docs(python): Fix id usage by @helderco in https://github.com/dagger/dagger/pull/3941
* sdk: python: Import API types into global namespace by @helderco in https://github.com/dagger/dagger/pull/3940
* Still set DAGGER_RUNNER_HOST from SDKs for now. by @sipsma in https://github.com/dagger/dagger/pull/3948
* Python: Add tests for improved Provisioning Error Messages by @KGB33 in https://github.com/dagger/dagger/pull/3880

**Full Changelog**: https://github.com/dagger/dagger/compare/sdk/python/v0.1.1...sdk/python/v0.2.0

## v0.1.1

- 🐍 https://pypi.org/project/dagger-io/0.1.1
- 📝 https://dagger.io/blog/python-sdk
- 🎬 https://www.youtube.com/watch?v=c0bLWmi2B-4

### What Changed
* Initial support for Python SDK Engine & Client by @samalba
  * https://github.com/dagger/dagger/commit/4acac2c153211f4fc430afd46c16e13df35f40d3
  * https://github.com/dagger/dagger/commit/40515fe917c8e8e970f719bc990b568e23b9ca8d
  * https://github.com/dagger/dagger/commit/9804c447845c05aa72403c122b0c029047a44ee7
  * https://github.com/dagger/dagger/commit/fa37be53c4e2e4ef1c659bd196ed06f9985e29eb
  * https://github.com/dagger/dagger/commit/de948bbda8b9060b26e16ba0d84be525da24124b
  * https://github.com/dagger/dagger/commit/99835f34296b3940d921d9338bf4c51500ef8f06
* Python SDK improvements by @samalba in https://github.com/dagger/dagger/pull/3222
* Guard against wrong dagger binary in Python SDK by @gerhard in https://github.com/dagger/dagger/pull/3310
* Python: Code-only schema by @helderco in https://github.com/dagger/dagger/pull/3299
* dagger.json by @sipsma in https://github.com/dagger/dagger/pull/3281
* Python: update examples to new API by @helderco in https://github.com/dagger/dagger/pull/3405
* Python: Support async resolvers and client by @helderco in https://github.com/dagger/dagger/pull/3301
* sdk: python: add codegen client by @helderco in https://github.com/dagger/dagger/pull/3460
* sdk: python: Refactor client factory by @helderco in https://github.com/dagger/dagger/pull/3636
* sdk: python: Add a few more tests by @helderco in https://github.com/dagger/dagger/pull/3656
* sdk: python: Remove wrapping result object by @helderco in https://github.com/dagger/dagger/pull/3713
* sdk: python: Revert to black’s default max line length by @helderco in https://github.com/dagger/dagger/pull/3714
* ci: basic Python support by @aluzzardi in https://github.com/dagger/dagger/pull/3704
* sdk: python: Fix provisioner by @helderco in https://github.com/dagger/dagger/pull/3716
* sdk: python: Revert test optimization by @helderco in https://github.com/dagger/dagger/pull/3732
* sdk: python: Add sync support by @helderco in https://github.com/dagger/dagger/pull/3718
* ci: Python SDK Publish by @helderco in https://github.com/dagger/dagger/pull/3730
* ci: Only publish Python SDK when there is an sdk/python tag by @gerhard in https://github.com/dagger/dagger/pull/3743
* chore: Add release instructions for the Python SDK by @gerhard in https://github.com/dagger/dagger/pull/3746
* Centralize more provisioning logic to be in helper.  by @sipsma in https://github.com/dagger/dagger/pull/3740
* sdk: python: Quick pyproject update by @helderco in https://github.com/dagger/dagger/pull/3747
* docs: Add Python SDK docs by @vikram-dagger in https://github.com/dagger/dagger/pull/3658
* docs: Updated Python SDK installation step by @vikram-dagger in https://github.com/dagger/dagger/pull/3766
* Fix typo in Python SDK's README.md by @charliermarsh in https://github.com/dagger/dagger/pull/3762
* sdk: python: Move examples into sdk’s path by @helderco in https://github.com/dagger/dagger/pull/3769
* docs: Make first example give immediate feedback by @jpadams in https://github.com/dagger/dagger/pull/3773
* docs: Minor addition to get started guide by @vikram-dagger in https://github.com/dagger/dagger/pull/3775
* Add windows engine-session binaries to engine image. by @sipsma in https://github.com/dagger/dagger/pull/3750
* docs: indicate that config is optional by @jpadams in https://github.com/dagger/dagger/pull/3777
* sdk: python: Prepare for release by @helderco in https://github.com/dagger/dagger/pull/3776

### New Contributors
* @charliermarsh made their first contribution in https://github.com/dagger/dagger/pull/3762
