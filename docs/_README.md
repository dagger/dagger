# FAQ

The intent behind this README is to answer contributor questions regarding [docs.dagger.io](https://docs.dagger.io).

## What is the structure of the documentation in the repository?

The documentation website (source code, assets and content) live in the `/docs` directory.

Within this directory, the content is separated into:

- `/current_docs`: the current docs shown on docs.dagger.io
- `/versioned_docs`: the next version(s) of the docs
- `/archived_docs`: the site template for the docs archive. Related instructions are in [ARCHIVING.md](./ARCHIVING.md)

## What happens to a new doc page after the PR gets merged?

It gets automatically deployed to [devel.docs.dagger.io](https://devel.docs.dagger.io).

The doc URL will use the `slug` property from the doc markdown metadata.

Given `slug: /1001/install/`, the live URL will be [devel.docs.dagger.io/1001/install](https://devel.docs.dagger.io/1001/install)

It must be manually deployed to [docs.dagger.io](https://docs.dagger.io).

## How can I test my docs change/PR?

### Locally

You will need to have `npm` and Node.js v18 installed.

From the `/docs` directory, run the following command: `npm install && npm start`

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
