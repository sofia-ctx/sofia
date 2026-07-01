<?php

namespace Fixture\Api;

class Thing extends Base
{
    use TraitA;
    use TraitB;

    public function own(): void {}

    // Overrides the same-named method in TraitA and Base (own wins).
    public function shared(): string
    {
        return 'thing';
    }
}
