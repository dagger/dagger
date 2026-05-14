<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};
use Dagger\NetworkProtocol;

#[DaggerObject] class EnumKind
{
    #[DaggerFunction] public function oppositeNetworkProtocol(
        NetworkProtocol $arg
    ): NetworkProtocol {
        return match ($arg) {
            NetworkProtocol::TCP => NetworkProtocol::UDP,
            NetworkProtocol::UDP => NetworkProtocol::TCP,
        };
    }

    #[DaggerFunction] public function increasePriority(
        Priority $priority,
    ): Priority {
        return match ($priority) {
            Priority::Low => Priority::Medium,
            default => Priority::High,
        };
    }

    #[DaggerFunction] public function toggleTodo(
        Task $task,
    ): Task {
        return match ($task) {
            Task::Todo => Task::Done,
            Task::Done => Task::Todo,
        };
    }
}
