<?php

// phpcs:disable PSR1.Files.SideEffects.FoundWithSymbols

declare(strict_types=1);

namespace Dagger;

/**
 * The core API, in the session the module or program runs in.
 */
function core(): Client
{
    return Dagger::getClientInstance();
}

// Generation keeps dag.php only for a module that has the global client.
if (is_file(__DIR__ . '/dag.php')) {
    require_once __DIR__ . '/dag.php';
}
