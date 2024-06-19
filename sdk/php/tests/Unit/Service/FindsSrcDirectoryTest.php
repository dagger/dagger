<?php

namespace Dagger\Tests\Unit\Service;

use Dagger\Client;
use Dagger\Service\DecodesValue;
use Dagger\Service\FindsSrcDirectory;
use Generator;
use org\bovigo\vfs\vfsStream;
use org\bovigo\vfs\vfsStreamDirectory;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RuntimeException;

#[Group('unit')]
#[CoversClass(FindsSrcDirectory::class)]
class FindsSrcDirectoryTest extends TestCase
{
    #[Test]
    public function itThrowsExceptionIfItCannotFindSrc(): void
    {
        $root = (vfsStream::setup())->url();

        $sut = new FindsSrcDirectory();

        self::expectException(RuntimeException::class);

        $sut($root);
    }

    #[Test]
    #[DataProvider('provideSrcDirectories')]
    public function itFindsModuleSrcDirectory(
        string $expected,
        array $structure,
        string $dir,
    ): void {
        $root = (vfsStream::setup(structure: $structure))->url();

        $sut = new FindsSrcDirectory();

        self::assertSame(
            sprintf('%s/%s', $root, $expected),
            $sut(sprintf('%s/%s', $root, $dir))
        );
    }

    /**
     * @return \Generator<array{
     *     0: string,
     *     1: array,
     *     2: string,
     * }>
     */
    public static function provideSrcDirectories(): Generator
    {
        $case = fn(string $startFrom, string $searchFor) => sprintf(
            'start from "%s", search for "%s"',
            $startFrom,
            $searchFor,
        );

        yield $case('/', '/src') => [
            'src',
            [
                'src' => [],
                'dagger' => 'Dagger file',
            ],
            '',
        ];

        yield $case('/src', '/src') => [
            'src',
            [
                'src' => [],
                'dagger' => 'Dagger file',
            ],
            'src',
        ];

        yield $case('/vendor/bin', '/src') => [
            'src',
            [
                'src' => [],
                'vendor' => [
                    'bin' => [],
                ],
                'dagger' => 'Dagger file',
            ],
            'vendor/bin',
        ];

        yield $case('/git/dagger-test', '/git/dagger-test/src') => [
            'git/dagger-test/src',
            [
                'git' => [
                    'dagger-test' => [
                        'src' => [],
                        'dagger' => 'Dagger file',
                    ],
                ],
            ],
            'git/dagger-test',
        ];

        yield $case('/git/dagger-test/vendor/bin', '/git/dagger-test/src') => [
            'git/dagger-test/src',
            [
                'git' => [
                    'dagger-test' => [
                        'src' => [],
                        'vendor' => [
                            'bin' => [],
                        ],
                        'dagger' => 'Dagger file',
                    ],
                ],
            ],
            'git/dagger-test/vendor/bin',
        ];
    }
}
