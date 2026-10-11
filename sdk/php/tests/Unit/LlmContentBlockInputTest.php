<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit;

use Dagger\Bytes;
use Dagger\LlmContentBlockInput;
use Dagger\LlmContentBlockKind;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(LlmContentBlockInput::class)]
class LlmContentBlockInputTest extends TestCase
{
    #[Test]
    public function itAcceptsAListOfToolResultContentBlocks(): void
    {
        $text = new LlmContentBlockInput(
            LlmContentBlockKind::TEXT, 'caption', '', '', null, false, '',
            '', null, null, null,
        );
        $image = new LlmContentBlockInput(
            LlmContentBlockKind::IMAGE, '', '', '', null, false, '',
            'image/png', new Bytes('aW1hZ2U='), null, null,
        );
        foreach ([null, [], [$text], [$text, $image]] as $content) {
            $result = new LlmContentBlockInput(
                LlmContentBlockKind::TOOL_RESULT, '', 'call-id', '', null, false, '',
                '', null, null, $content,
            );

            self::assertSame($content, $result->content);
        }
    }
}
