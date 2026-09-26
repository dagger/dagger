<?php

declare(strict_types=1);

namespace Dagger\Exception;

/**
 * @internal this should never occur, outside of tests.
 *           It indicates a fault in the SDK's logic.
 */
final class SDKBug extends \LogicException
{
}
