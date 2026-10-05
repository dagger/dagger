<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

/**
 * Deliberately not a DaggerObject: FindsDaggerObjects must ignore it. It stands
 * in for the parent object EntrypointCommand serialises between calls, which is
 * where enums are actually nested rather than serialised on their own.
 */
class EnumHolder
{
    public StringBackedEnum $word;

    public ?StringBackedEnum $maybe = null;

    public PureEnum $pure;
}
