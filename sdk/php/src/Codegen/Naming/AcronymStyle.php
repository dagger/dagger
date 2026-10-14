<?php

declare(strict_types=1);

namespace Dagger\Codegen\Naming;

/**
 * How acronyms and terms are written where a word starts with a capital:
 * a value of the engine's AcronymStyle enum.
 */
enum AcronymStyle: string
{
    /** The standard spelling: HTTPClient. */
    case UPPERCASE = 'UPPERCASE';
    /** Written like a word: HttpClient. */
    case CAPITALIZED = 'CAPITALIZED';
}
