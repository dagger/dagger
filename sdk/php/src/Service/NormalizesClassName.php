<?php

declare(strict_types=1);

namespace Dagger\Service;

final readonly class NormalizesClassName
{
    public static function trimLeadingNamespace(string $name): string
    {
        return preg_replace('#^\\\\?[^\\\\]+\\\\#', '', $name, 1);
    }

    public static function shorten(string $name): string
    {
        return preg_replace('#[^\\\\]+\\\\#', '', $name);
    }
}
