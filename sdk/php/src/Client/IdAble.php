<?php

namespace Dagger\Client;

use Dagger\Id;

interface IdAble
{
    public function id(): Id;
}
