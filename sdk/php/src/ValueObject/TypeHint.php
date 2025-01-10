<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

interface TypeHint
{
    public function getName(): string;
    public function getTypeDefKind(): \Dagger\TypeDefKind;
    public function isNullable(): bool;
}
