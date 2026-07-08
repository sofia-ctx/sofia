<?php

namespace App\Domain;

final class User
{
    public function __construct(private string $email)
    {
    }

    public function email(): string
    {
        return $this->email;
    }
}
