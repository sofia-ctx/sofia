<?php

namespace Fixture\Api;

class Base extends GrandBase
{
    public function base(): void {}

    // Outranked by both Thing::shared and TraitA::shared.
    public function shared(): string
    {
        return 'base';
    }

    // Must be excluded from the public surface.
    protected function hidden(): void {}
}
