<?php

$finder = (new PhpCsFixer\Finder())
    ->in(__DIR__)
    ->notPath('src/Connection/version.php')
    ->exclude([
        'generated'
    ])
;

return (new PhpCsFixer\Config())
    ->setRules([
        '@Symfony' => true,
        'global_namespace_import' => true,
    ])
    ->setFinder($finder)
;
