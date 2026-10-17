<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Unsupported;

/**
 * Deliberately missing the DaggerObject attribute.
 */
enum UnattributedEnum: string
{
    case Foo = 'first';
}
