<?php

namespace DaggerIo\Client;

abstract class AbstractDaggerInputObject
{
    public function toArray(): array
    {
        return (array) $this;
    }
}
