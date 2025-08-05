<?php

declare(strict_types=1);

namespace Dagger\Exception;

final class MissingAttribute extends \RuntimeException
{
    public static function returnsListOfType(
        string $methodName,
    ): self {
        $missingAttribute = \Dagger\Attribute\ReturnsListOfType::class;

        return new self(
            "DaggerFunction '$methodName' requires $missingAttribute"
            . ', this is because it has an array return type',
        );
    }
}
