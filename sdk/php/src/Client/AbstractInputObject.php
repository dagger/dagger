<?php

namespace Dagger\Client;

abstract class AbstractInputObject
{
    public function toArray(): array
    {
        return (array) $this;
    }
}
