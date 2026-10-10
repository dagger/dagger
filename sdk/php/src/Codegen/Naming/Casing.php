<?php

declare(strict_types=1);

namespace Dagger\Codegen\Naming;

/**
 * Conventions for joining words into an identifier: a value of the engine's
 * Casing enum.
 *
 * See hack/designs/identifier-casing.md, "Formatting", in the Dagger repository.
 */
enum Casing: string
{
    /** Every word in its capitalized form: HTTPAPIClient. */
    case PASCAL = 'PASCAL';
    /** The first word lowercase, the rest capitalized: httpAPIClient. */
    case CAMEL = 'CAMEL';
    /** Lowercase, joined with "_": http_api_client. */
    case SNAKE = 'SNAKE';
    /** Uppercase, joined with "_": HTTP_API_CLIENT. */
    case SCREAMING_SNAKE = 'SCREAMING_SNAKE';
    /** Lowercase, joined with "-": http-api-client. */
    case KEBAB = 'KEBAB';
    /** Lowercase, no separator: httpapiclient. */
    case FLAT = 'FLAT';
}
