<?php

declare(strict_types=1);

namespace Dagger\Module;

use Dagger\Service\FindsDaggerObjects;
use Dagger\ValueObject\DaggerEnum;
use Dagger\ValueObject\DaggerObject;

final readonly class Description
{
    /**
     * @param list<DaggerObject> $objects
     * @param list<DaggerEnum> $enums
     */
    public function __construct(
        public array $objects = [],
        public array $enums = [],
    ) {
    }

    public static function fromDirectory(string $directory): self
    {
        $found = (new FindsDaggerObjects())($directory);

        // The directory walk has no defined order, and a second generation must
        // write the same bytes.
        usort($found, static fn($a, $b) => strcmp($a->name, $b->name));

        return new self(
            array_values(array_filter($found, static fn($d) => $d instanceof DaggerObject)),
            array_values(array_filter($found, static fn($d) => $d instanceof DaggerEnum)),
        );
    }
}
