-- Сгенерировано из docs/sql-functions.md — при изменении функций правьте документацию и этот файл синхронно.

-- ===== Таблицы =====

CREATE TYPE session_status AS ENUM ('LOBBY_OPEN', 'IN_PROGRESS', 'FINISHED');

CREATE TABLE Users (
    user_id       serial PRIMARY KEY,
    login         varchar   NOT NULL UNIQUE,
    password_hash varchar   NOT NULL,
    last_active   timestamp
);

CREATE TABLE Game_sessions (
    session_id  serial PRIMARY KEY,
    started_at  timestamp      NOT NULL DEFAULT now(),
    status      session_status NOT NULL DEFAULT 'LOBBY_OPEN',
    turn_amount integer        NOT NULL CHECK (turn_amount >= 1)
);

CREATE TABLE Combinations (
    combo_id serial PRIMARY KEY,
    name     varchar NOT NULL UNIQUE,
    points   integer NOT NULL
);

CREATE TABLE Players (
    player_id    serial PRIMARY KEY,
    user_id      integer NOT NULL REFERENCES Users (user_id)
                         ON UPDATE CASCADE ON DELETE CASCADE,
    session_id   integer NOT NULL REFERENCES Game_sessions (session_id)
                         ON UPDATE CASCADE ON DELETE CASCADE,
    turn_at      integer NOT NULL,
    total_points integer NOT NULL DEFAULT 0,
    UNIQUE (user_id, session_id),
    UNIQUE (session_id, turn_at) DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE Turns (
    turn_id     serial PRIMARY KEY,
    player_id   integer NOT NULL REFERENCES Players (player_id)
                        ON UPDATE CASCADE ON DELETE CASCADE,
    combo_id    integer REFERENCES Combinations (combo_id)
                        ON UPDATE CASCADE ON DELETE SET NULL,
    turn_number integer NOT NULL,
    throw_number integer NOT NULL,
    is_closed   boolean NOT NULL DEFAULT FALSE,
    UNIQUE (player_id, turn_number)
);

CREATE TABLE Dice (
    die_id   serial PRIMARY KEY,
    turn_id  integer NOT NULL REFERENCES Turns (turn_id)
                     ON UPDATE CASCADE ON DELETE CASCADE,
    value    integer NOT NULL,
    is_fixed boolean NOT NULL DEFAULT FALSE
);

-- ===== CHECK-ограничения =====

-- trg_dice_value_range
ALTER TABLE dice
    ADD CONSTRAINT dice_value_range CHECK (value BETWEEN 1 AND 6);
