<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen\Naming;

use Dagger\Codegen\Naming\AcronymStyle;
use Dagger\Codegen\Naming\Casing;
use Dagger\Codegen\Naming\FormattedNames;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(FormattedNames::class)]
class FormattedNamesTest extends TestCase
{
    #[Test]
    public function itLooksUpNamesByFormat(): void
    {
        $names = FormattedNames::fromArray([
            'CAMEL:CAPITALIZED' => ['withGPU' => 'withGpu', '_internal' => 'internal'],
            'SCREAMING_SNAKE:UPPERCASE' => ['PerSession' => 'PER_SESSION'],
        ]);

        self::assertSame('withGpu', $names->format('withGPU', Casing::CAMEL, AcronymStyle::CAPITALIZED));
        self::assertSame('PER_SESSION', $names->format('PerSession', Casing::SCREAMING_SNAKE));

        // a format or name the engine didn't format
        self::assertNull($names->format('withGPU', Casing::CAMEL));
        self::assertNull($names->format('notInSchema', Casing::CAMEL, AcronymStyle::CAPITALIZED));
        // internal names keep their underscore
        self::assertNull($names->format('_internal', Casing::CAMEL, AcronymStyle::CAPITALIZED));
        self::assertNull((new FormattedNames())->format('withGPU', Casing::CAMEL, AcronymStyle::CAPITALIZED));
    }

    #[Test]
    public function itReadsTheNamesFile(): void
    {
        $names = FormattedNames::fromArray([
            'CAMEL:CAPITALIZED' => ['withGPU' => 'withGpu', 'bad' => 1, 'empty' => ''],
            'PASCAL:UPPERCASE' => 'not a map',
        ]);

        self::assertSame(['CAMEL:CAPITALIZED' => ['withGPU' => 'withGpu']], $names->toArray());
        self::assertSame([], FormattedNames::fromArray([])->toArray());
        self::assertSame(
            'SCREAMING_SNAKE:UPPERCASE',
            FormattedNames::key(Casing::SCREAMING_SNAKE, AcronymStyle::UPPERCASE),
        );
    }
}
