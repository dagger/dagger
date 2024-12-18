# FAQ

The intent behind this README is to answer contributor questions regarding [docs.dagger.io](https://docs.dagger.io).

## What is the structure of the documentation in the repository?

The documentation website (source code, assets and content) live in the `/docs` directory.

Within this directory, the content is separated into:

- `/current_docs`: the current docs shown on docs.dagger.io
- `/versioned_docs`: the next version(s) of the docs, if available
- `/archived_docs`: the site template for the docs archive. Related instructions are in [ARCHIVING.md](./ARCHIVING.md)

## What happens to a new doc page after the PR gets merged?

It gets automatically deployed to [devel.docs.dagger.io](https://devel.docs.dagger.io).

You can use this staging website to test the documentation, including:

- verifying that the new content appears in the navigation
- verifying internal and external links work correctly
- verifying that images appear correctly
- etc.

The doc URL will use the `slug` property from the doc markdown metadata. Given
`slug: /1001/install/`, the live URL will be [devel.docs.dagger.io/1001/install](https://devel.docs.dagger.io/1001/install)

It must be manually deployed to [docs.dagger.io](https://docs.dagger.io). Only
a certain group of people can deploy via Netlify. For those with permission,
follow these steps:

1. Log in to the [Netlify dashboard for https://docs.dagger.io](https://app.netlify.com/sites/docs-dagger-io).
2. Refer to the list of "production deploys" and select the one you wish to
   deploy. Usually, this will be the most recent one. You can confirm this by
   checking the deployment hash against the latest commit hash in the
   [dagger/dagger repository main branch](https://github.com/dagger/dagger).
3. On the deployment page, click the "Preview" button to once again
   preview/check the deployment. You can also check the deployment log to
   confirm there were no errors during the documentation build process.
4. If you are satisfied with the preview, click the "Publish deploy" button.
   This will publish the selected deployment on <https://docs.dagger.io>.

> [!NOTE]
>
> There have been cases where Netlify builds have failed with errors,
> but the same build succeeds when performed locally. In the past, one reason
> for this has been Netlify's use of a stale cache. In case you encounter
> this error, click "Options -> Clear cache and retry with latest branch commit"
> to recreate the deployment with a clean cache.

## How can I test my docs change/PR?

### Locally

You will need to have `yarn` and Node.js v18 installed.

From the `/docs` directory, run the following command: `yarn install && yarn start`

This will install all dependencies, start the docs web server locally and open [localhost:3000](http://localhost:3000/) in your browser.

### With a Dagger module

```console
# test PR 7422
dagger call -m github.com/dagger/dagger@pull/7422/head --source https://github.com/dagger/dagger#pull/7422/head docs server as-service up

## get markdown lint report for PR 7422
dagger call -m github.com/dagger/dagger/linters/markdown \
 lint --source https://github.com/dagger/dagger#pull/7422/head \
 json
```

## What else should I keep in mind as I add new doc pages?

- ["I would like the docs for http://dagger.io to be world-class… Any recommendations or advice?"](https://twitter.com/solomonstre/status/1460676168001077252) - Solomon, Nov. 2021
- "I would propose starting off with common use case and get a feedback loop possible where customers get to somewhat steer the topics they want next. Maybe via a vote system to prioritise . The community leads it all." [Frankie Onuonga via Twitter, Nov. 2021](https://twitter.com/FrankieOnuonga/status/1460677907093897219)
- [The Documentation System](https://documentation.divio.com/) +1 from @samalba
- [Maybe it’s time we re-think docs](https://kathykorevec.medium.com/building-a-better-place-for-docs-197f92765409) - Kathy Korevec, Jun. 2021
- 🎙 [Ship It #17: Docs are not optional](https://changelog.com/shipit/17) - Kathy Korevec, Aug. 2021
- 📚 [Working Backwards](https://www.amazon.co.uk/dp/1529033829) - Colin Bryar & Bill Carr, Feb. 2021
- 🎬 [LeadDevBerlin: Writing effective documentation](https://youtu.be/R6zeikbTgVc?t=19) - Beth Aitman, Dec. 2019
- 🎬 [DocOps: engineering great documentation](https://youtu.be/AnvqMb1VT40) - Adam Butler, Dec. 2017
- 🎬 [PyCon: Writing great documentation](https://www.youtube.com/watch?v=z3fRu9pkuXE) - Jacob Kaplan-Moss, Sep. 2014
