<?php

namespace App\Application;

use App\Domain\User;

final class RegisterUser
{
    public function __invoke(string $email): User
    {
        return new User($email);
    }
}
