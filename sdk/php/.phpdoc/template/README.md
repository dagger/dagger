This directory matches file structure of phpDocumentor's [default template](https://github.com/phpDocumentor/phpDocumentor/tree/master/data/templates/default).

Any files with the same name, will override that part of the default template.

A [custom.css.twig](css/custom.css.twig) file overrides the default styles to look more like [Dagger Docs](https://docs.dagger.io/).

[element-found-in](components/element-found-in.html.twig)
has been overriden to avoid printing line numbers. Line numbers cause unnecessary churn in code reviews, from even the smallest of edits.

[class-titles](components/class-title.html.twig), [enum-titles](components/enum-title.html.twig), [interface-titles](components/interface-title.html.twig), [table-of-contents](components/table-of-contents.html.twig) and [sidebar](components/sidebar.html.twig) have been overriden to remove links to a "Packages" section. Dagger is not installed as a composer package, so all links and pages relating to packages have been removed to avoid confusion.

The [sidebar](components/sidebar.html.twig) has also been overriden other unnecessary sections:
- Reports (deprecations and errors which are present on each class anyway).
- Indices (a list of file names).
