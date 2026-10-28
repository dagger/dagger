<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Unsupported;

use Dagger\Attribute;

/**
 * Lives outside tests/Unit/Fixture so that FindsDaggerObjects, which scans that
 * directory, never tries to build a value object from it.
 */
#[Attribute\DaggerObject]
enum CaselessBackedEnum: string
{
}
