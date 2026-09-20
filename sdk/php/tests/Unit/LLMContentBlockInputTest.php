<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit;

use Dagger\LLMContentBlockInput;
use Dagger\LLMContentBlockKind;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(LLMContentBlockInput::class)]
class LLMContentBlockInputTest extends TestCase
{
    #[Test]
    public function itAcceptsAListOfToolResultContentBlocks(): void
    {
        $text = new LLMContentBlockInput(
            LLMContentBlockKind::TEXT, 'caption', '', '', null, false, '',
            '', '', null, null,
        );
        $image = new LLMContentBlockInput(
            LLMContentBlockKind::IMAGE, '', '', '', null, false, '',
            'image/png', 'aW1hZ2U=', null, null,
        );
        foreach ([null, [], [$text], [$text, $image]] as $content) {
            $result = new LLMContentBlockInput(
                LLMContentBlockKind::TOOL_RESULT, '', 'call-id', '', null, false, '',
                '', '', null, $content,
            );

            self::assertSame($content, $result->content);
        }
    }
}
