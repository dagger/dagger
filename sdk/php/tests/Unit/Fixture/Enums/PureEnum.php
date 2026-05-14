<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Enums;

// Pure (non-backed) enum — should be rejected with a clear UnsupportedType error.
enum PureEnum
{
    case Foo;
    case Bar;
}
