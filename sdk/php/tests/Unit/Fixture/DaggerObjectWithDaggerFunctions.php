<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\DefaultPath;
use Dagger\Attribute\Doc;
use Dagger\Attribute\Ignore;
use Dagger\Container;
use Dagger\Directory;
use Dagger\File;
use Dagger\Json;
use Dagger\ValueObject;

#[DaggerObject]
final class DaggerObjectWithDaggerFunctions
{
    #[DaggerFunction]
    public function __construct() {
    }

    #[DaggerFunction]
    public function returnBool(): bool
    {
        return true;
    }

    #[DaggerFunction('this method returns 1')]
    public function returnInt(): int
    {
        return 1;
    }

    #[DaggerFunction]
    public function returnString(): string
    {
        return 'hello';
    }

    #[DaggerFunction]
    public function requiredBool(bool $value): void {
    }

    #[DaggerFunction]
    public function requiredInt(int $value): void
    {
    }

    #[DaggerFunction]
    public function requiredString(string $value): void
    {
    }

    #[DaggerFunction]
    public function implicitlyOptionalString(?string $value): void {
    }

    #[DaggerFunction]
    public function explicitlyOptionalString(?string $value = null): void
    {
    }

    #[DaggerFunction]
    public function stringWithDefault(?string $value = 'test'): void
    {
    }

    #[DaggerFunction]
    public function annotatedString(
        #[Doc('this value should have a description')]
        string $value
    ): void {
    }

    #[DaggerFunction]
    public function requiredStrings(string $first, string $second): void
    {
    }

    #[DaggerFunction]
    public function stringsWithDefaults(
        string $first = 'first',
        string $second = 'second',
    ): void {
    }

    #[DaggerFunction]
    public function implicitlyOptionalContainer(?Container $value): void
    {
    }

    #[DaggerFunction]
    public function explicitlyOptionalFile(?File $value): void
    {
    }

    #[DaggerFunction]
    public function fileWithDefaultPath(
        #[DefaultPath('./test')]
        File $value
    ): void {
    }


    #[DaggerFunction]
    public function directoryWithIgnore(
        #[DefaultPath('.')]
            #[Ignore('vendor/', 'generated/', 'env')]
        Directory $value
    ): void {
    }

    public function notADaggerFunction(): string {
        return 'DaggerFunctions MUST have the DaggerFunction Attribute';
    }

    #[DaggerFunction]
    private function privateDaggerFunction(): string {
        return 'DaggerFunctions MUST be public';
    }

    public static function getValueObjectEquivalent(): ValueObject\DaggerObject
    {
        return new ValueObject\DaggerObject(
            DaggerObjectWithDaggerFunctions::class,
            '',
            [
                new ValueObject\DaggerFunction(
                    '',
                    null,
                    [],
                    new ValueObject\TypeHint\Type(self::class)
                ),
                new ValueObject\DaggerFunction(
                    'returnBool',
                    null,
                    [],
                    new ValueObject\TypeHint\Type('bool')
                ),
                new ValueObject\DaggerFunction(
                    'returnInt',
                    'this method returns 1',
                    [],
                    new ValueObject\TypeHint\Type('int')
                ),
                new ValueObject\DaggerFunction(
                    'returnString',
                    null,
                    [],
                    new ValueObject\TypeHint\Type('string')
                ),
                new ValueObject\DaggerFunction(
                    'requiredBool',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type('bool'),
                            null,
                        )
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'requiredInt',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type('int'),
                            null,
                        )
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'requiredString',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type('string'),
                            null,
                        )
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'implicitlyOptionalString',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type('string', true),
                            new Json('null'),
                        )
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'explicitlyOptionalString',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type('string', true),
                            new Json('null'),
                        )
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'stringWithDefault',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type('string', true),
                            new Json('"test"'),
                        )
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'annotatedString',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            'this value should have a description',
                            new ValueObject\TypeHint\Type('string'),
                            null,
                        ),
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'requiredStrings',
                    null,
                    [
                        new ValueObject\Argument(
                            'first',
                            '',
                            new ValueObject\TypeHint\Type('string'),
                            null,
                        ),
                        new ValueObject\Argument(
                            'second',
                            '',
                            new ValueObject\TypeHint\Type('string'),
                            null,
                        ),
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'stringsWithDefaults',
                    null,
                    [
                        new ValueObject\Argument(
                            'first',
                            '',
                            new ValueObject\TypeHint\Type('string'),
                            new Json('"first"'),
                        ),
                        new ValueObject\Argument(
                            'second',
                            '',
                            new ValueObject\TypeHint\Type('string'),
                            new Json('"second"'),
                        )
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'implicitlyOptionalContainer',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type(Container::class, true),
                            new Json('null'),
                        ),
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'explicitlyOptionalFile',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type(File::class, true),
                            new Json('null'),
                        ),
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'fileWithDefaultPath',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type(File::class, false),
                            null,
                            './test',
                        ),
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
                new ValueObject\DaggerFunction(
                    'directoryWithIgnore',
                    null,
                    [
                        new ValueObject\Argument(
                            'value',
                            '',
                            new ValueObject\TypeHint\Type(Directory::class, false),
                            null,
                            '.',
                            ['vendor/', 'generated/', 'env'],
                        ),
                    ],
                    new ValueObject\TypeHint\Type('void'),
                ),
            ]
        );
    }
}
