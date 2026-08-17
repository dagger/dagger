<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Codegen;

use Dagger\Codegen\Introspection\IntrospectionSchema;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(IntrospectionSchema::class)]
class IntrospectionSchemaTest extends TestCase
{
    #[Test]
    public function itReadsTheSchemaVersion(): void
    {
        $schema = IntrospectionSchema::fromArray([
            '__schemaVersion' => 'v1.0.0-beta.9',
            '__schema' => ['types' => []],
        ]);

        self::assertSame('v1.0.0-beta.9', $schema->version);
    }

    #[Test]
    #[DataProvider('nullableObjectVersions')]
    public function itChecksNullableObjectVersionSupport(?string $version, bool $expected): void
    {
        $schema = IntrospectionSchema::fromArray([
            '__schemaVersion' => $version,
            '__schema' => ['types' => []],
        ]);

        self::assertSame($expected, $schema->supportsNullableObjects());
    }

    public static function nullableObjectVersions(): iterable
    {
        yield 'missing version' => [null, true];
        yield 'development version' => ['development', true];
        yield 'old development version' => ['v0.21.0-dev', false];
        yield 'beta 9 development version' => ['v1.0.0-beta.9-dev', false];
        yield 'beta 10' => ['v1.0.0-beta.10', true];
        yield 'beta 10 development version' => ['v1.0.0-beta.10-dev', true];
        yield 'release candidate' => ['v1.0.0-rc.1', true];
        yield 'stable version' => ['v1.0.0', true];
    }
}
