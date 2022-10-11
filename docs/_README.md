# FAQ

The intent behind this README is to answer contributor questions regarding [docs.dagger.io](https://docs.dagger.io).

## What happens to a new doc page after the PR gets merged?

It gets automatically deployed to [docs.dagger.io](https://docs.dagger.io).

The doc URL will use the `slug` property from the doc markdown metadata.

Given `slug: /1001/install/`, the live URL will be [docs.dagger.io/1001/install](https://docs.dagger.io/1001/install)

## How can I run docs locally?

You will need to have `yarn` and Node.js v16 installed.

From the root of the repo run the following command: `make web`

This will install all dependencies, start the docs web server locally and open [localhost:3000](http://localhost:3000/) in your browser.

## How can I add a new doc page?

```bash
.
├── docs/
│   └── new.sh            1. $ ./new.sh my-doc-title
├── Makefile (repo root)  2. $ make web
└── website/              3. $ npx docusaurus build
```

1. From the `docs` dir, run `./new.sh my-doc-title`
   This will create a new Markdown file for the new doc page with a random ID, e.g `docs/f1a2c-my-doc-title.md`

2. After executing the `./new.sh` command, make sure to previsualize the new doc by running the `make web` command from the root directory. This will trigger `docusaurus start`, [creating a local dev server](https://docusaurus.io/docs/cli#docusaurus-start-sitedir).  
   `make web` **is required** when creating a new doc because it exports a new `_redirects` file, located in `/website/static/_redirects`. This is a configuration file that maps the docs ID with their respective filename, so every doc can also be reached with only the UUID in the URL.  
   Try it out and you'll see how `https://docs.dagger.io/1247` redirects to `https://docs.dagger.io/1247/dagger-fs`  
   Don't worry if the redirection doesn't work in your new doc, as it's a server-side implementation that will only take effect in production. Just make sure it has the same format as the rest of the mappings in the `_redirects` file.

3. Once created and previsualized, run `npx docusaurus build` from the `/website` directory. This command verifies no links are broken when parsing markdown, among other things, so it's a good way to "test" your new doc.

This new doc will not be added to the navigation.
We prefer to keep the organisation of doc pages, and writing them separate.
For the time being - 2022 Q1 - the focus is on writing self-contained doc content.
Don't worry about where to fit this content, it's enough to keep this in mind: [Writing effective documentation](https://www.youtube.com/watch?v=R6zeikbTgVc&t=19s).

## Debugging

A [debug plugin](https://docusaurus.io/docs/api/plugins/@docusaurus/plugin-debug) is available at `http://localhost:3000/__docusaurus/debug`.  
This is a great resource to help you solve common problems that show up in your terminal when starting a local dev server.

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
