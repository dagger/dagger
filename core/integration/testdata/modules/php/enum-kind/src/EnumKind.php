<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};
use Dagger\NetworkProtocol;

#[DaggerObject] class EnumKind
{
    #[DaggerFunction] public function oppositeNetworkProtocol(NetworkProtocol $arg): NetworkProtocol
    {
        return match ($arg) {
            NetworkProtocol::TCP => NetworkProtocol::UDP,
            NetworkProtocol::UDP => NetworkProtocol::TCP,
        };
    }
}
