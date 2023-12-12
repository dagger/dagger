<?php

namespace Dagger\Client;

abstract class AbstractDaggerInputObject
{
    public function toArray(): array
    {
        return (array) $this;
    }
}
