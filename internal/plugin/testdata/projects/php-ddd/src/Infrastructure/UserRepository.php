<?php

namespace App\Infrastructure;

use App\Domain\User;

final class UserRepository
{
    /** @var array<string, User> */
    private array $byEmail = [];

    public function save(User $user): void
    {
        $this->byEmail[$user->email()] = $user;
    }
}
