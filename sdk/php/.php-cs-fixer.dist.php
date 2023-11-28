<?php

$finder = (new PhpCsFixer\Finder())
    ->in(__DIR__)
    ->exclude('generated')
;

return (new PhpCsFixer\Config())
    ->setRules([
        '@Symfony' => true,
        'global_namespace_import' => true,
    ])
    ->setFinder($finder)
;
