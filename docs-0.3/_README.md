## How can I run docs locally?

You will need to have `yarn` and Node.js v18 installed.

From the `website` directory run the following commands:

```bash
yarn install
yarn start
```

This will install all dependencies, start the docs web server locally and open [localhost:3000](http://localhost:3000/) in your browser.

## How should I do markdown hyperlinks?

Since all docs have a unique id in Docusaurus no matter where they live on the filesystem (see adding a new doc below), we refer to other docs in links by their id and name like this:

```markdown
[Learn more about writing extensions](/bnzm7/writing_extensions)

```

This will make a link to a file called `bnzm7-writing_extensions` (that was created with `new.sh` at some point). In this case, the file lives in `docs/guides` but that doesn't matter. We can move it wherever and Docusaurus will find it. Take note of the leading `/` and how the `-` is changed to a path separator `/` as well.

## How can I add a new doc page?

```bash
.
├── docs/
│   └── new.sh        1. $ ./new.sh my-doc-title (generates random id)
│   └── guides/       2. $ mv r4nd0-my-doc-title guides/. (move & edit doc)
└── website/          3. $ yarn install && yarn start
    └── sidebars.js   4. (edit sidebars.js to slot your new guide into place)
                      5. $ yarn start (check things out for real!)
```

NOTE: you'll see some `.mdx` files around. They are markdown files that support JavaScript components. Most docs won't need these.

NOTE: docs support [Mermaid diagrams](https://mermaid-js.github.io/mermaid/#/)!

1. From the `docs` dir, run `./new.sh my-doc-title`
   This will create a new Markdown file for the new doc page with a random ID, e.g `docs/r4nd0-my-doc-title.md`

2. Move your shiny new doc into the `guides` directory.

3. Run `yarn install && yarn start` from the `/website` directory. This will download dependencies and also verifies no links are broken when parsing markdown, among other things, so it's a good way to "test" your new doc. Use `ctrl-c` to break out.

4. Edit the sidebars.js file in the `website` directory to slot your new guide into place. We'll undoubtedly move things around over time.

5. Run `yarn start` from the `/website` directory again to see your doc in the sidebar and working like a champ!
