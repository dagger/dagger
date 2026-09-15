<?php

declare(strict_types=1);

namespace Dagger;

function dag(): Client
{
    return Dagger::getClientInstance();
}
