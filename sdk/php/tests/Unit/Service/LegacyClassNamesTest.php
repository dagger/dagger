<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Service;

use Dagger\Service\LegacyClassNames;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(LegacyClassNames::class)]
class LegacyClassNamesTest extends TestCase
{
    private string $directory;

    protected function setUp(): void
    {
        $this->directory = sys_get_temp_dir() . DIRECTORY_SEPARATOR . uniqid('legacy-class-names-');
        mkdir($this->directory);
        touch($this->directory . DIRECTORY_SEPARATOR . 'Llm.php');
        touch($this->directory . DIRECTORY_SEPARATOR . 'SdkConfig.php');
    }

    protected function tearDown(): void
    {
        array_map('unlink', glob($this->directory . DIRECTORY_SEPARATOR . '*') ?: []);
        rmdir($this->directory);
    }

    #[Test]
    public function itFindsGeneratedFilesIgnoringCase(): void
    {
        $llm = $this->directory . DIRECTORY_SEPARATOR . 'Llm.php';
        self::assertSame($llm, LegacyClassNames::fileFor('Dagger\LLM', $this->directory));
        self::assertSame($llm, LegacyClassNames::fileFor('\Dagger\LLM', $this->directory));
        self::assertSame($llm, LegacyClassNames::fileFor('Dagger\Llm', $this->directory));
        self::assertSame(
            $this->directory . DIRECTORY_SEPARATOR . 'SdkConfig.php',
            LegacyClassNames::fileFor('Dagger\SDKConfig', $this->directory),
        );
    }

    #[Test]
    public function itOnlyFindsGeneratedClasses(): void
    {
        self::assertNull(LegacyClassNames::fileFor('Dagger\Container', $this->directory));
        self::assertNull(LegacyClassNames::fileFor('Other\LLM', $this->directory));
        self::assertNull(LegacyClassNames::fileFor('Dagger\Service\LLM', $this->directory));
        self::assertNull(LegacyClassNames::fileFor('Dagger\\', $this->directory));
    }

    #[Test]
    public function itLoadsGeneratedClassesUnderTheirOldSpelling(): void
    {
        // Renamed only in letter case, so the old spelling names the same
        // class, wherever its file is found.
        self::assertTrue(class_exists('Dagger\LLM'));
        self::assertSame('Dagger\Llm', (new \ReflectionClass('Dagger\LLM'))->getName());
    }
}
