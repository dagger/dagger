<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute\DaggerArgument;
use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;

#[DaggerObject]
final class ButterKnife
{
    #[DaggerFunction]
    public function spread(
        string $spread,
        #[DaggerArgument('The surface on which to spread')]
        string $surface
    ): bool {
        return true;
    }

    #[DaggerFunction('Nothing better')]
    public function sliceBread(): string
    {
        return 'bread';
    }

    public function accidentallyDropOnFloor(bool $hasFiveSecondsPassed): void
    {
    }
}
