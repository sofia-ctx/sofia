<?php

namespace Fixture\Api;

trait TraitA
{
    public function aa(): void {}

    // Outranks Base::shared but is outranked by Thing::shared.
    public function shared(): string
    {
        return 'traitA';
    }
}
