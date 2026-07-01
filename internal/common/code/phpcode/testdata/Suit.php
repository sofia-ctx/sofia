<?php

declare(strict_types=1);

namespace App\Card;

/** Standard playing-card suits (pure enum — no backing value). */
enum Suit
{
    case Hearts;
    case Diamonds;
    case Clubs;
    case Spades;

    public function color(): string
    {
        return match ($this) {
            self::Hearts, self::Diamonds => 'red',
            self::Clubs, self::Spades => 'black',
        };
    }
}
