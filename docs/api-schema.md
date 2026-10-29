# Poker API — схема взаимодействия

Чистая версия схемы из `Poker_API_extended.drawio`, собранная на Mermaid.

Связанные документы — правила игры в [`game-rules.md`](./game-rules.md), функции БД в [`sql-functions.md`](./sql-functions.md), схема данных — `poker_DB.drawio`.

**Участники**

| Обозначение | Роль |
|---|---|
| `U` | Пользователь — человек за интерфейсом |
| `C` | Клиент — фронтенд, хранит Auth Token |
| `S` | Сервер — вся бизнес-логика и валидация |
| `DB` | База данных |

Везде, где игрок уже авторизован, клиент прикладывает `Auth Token`, и проверяет его **сервер**, а не БД.

---

## Жизненный цикл партии

```mermaid
stateDiagram-v2
    [*] --> LOBBY_OPEN: создание лобби (turn_amount >= 1)
    LOBBY_OPEN --> LOBBY_OPEN: вход игрока (игроков < 4)
    LOBBY_OPEN --> IN_PROGRESS: начало игры (2..4 игрока)
    IN_PROGRESS --> IN_PROGRESS: ход игрока
    IN_PROGRESS --> FINISHED: все игроки сделали N ходов
    FINISHED --> [*]
```

Ход внутри `IN_PROGRESS`:

```mermaid
stateDiagram-v2
    throw1 : Бросок 1 (все 5 костей)
    fix : Фиксация костей (на клиенте)
    reroll : Переброс
    combo : Выбор комбинации

    [*] --> throw1: автоматически, INSERT Turn + 5 Dice
    throw1 --> fix: выбор is_fixed
    fix --> reroll: fixed_die_ids[], throw_number < 3
    reroll --> fix
    throw1 --> combo: выбрать combo_id
    reroll --> combo
    combo --> [*]: score + total_points
```

---

## 1. Авторизация / регистрация

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    U->>C: Авторизация / регистрация
    C->>S: Логин, Пароль
    S->>DB: login / password_hash (регистрация)
    Note over S,DB: Регистрация — сервер хеширует пароль перед INSERT.<br/>Вход — БД возвращает password_hash, пароль проверяется на сервере.
    DB-->>S: user_id
    S->>DB: SELECT лобби со status = LOBBY_OPEN
    DB-->>S: session_id, status (OPEN)
    S-->>C: Информация профиля + Auth Token
    S-->>C: Множество доступных лобби
    C-->>U: Список доступных лобби
```

## 2. Создание лобби

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    U->>C: Создание лобби, кол-во ходов
    C->>S: Auth Token + turn_amount
    Note over S: Токен проверяет сервер, turn_amount >= 1
    S->>DB: INSERT сессии с turn_amount
    DB-->>S: session_id, status
    S-->>C: Информация лобби
    C-->>U: Страница лобби
```

## 3. Поиск лобби

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    U->>C: Поиск лобби
    C->>S: Auth Token
    S->>DB: SELECT status = LOBBY_OPEN
    Note over S,DB: Возврат только лобби со статусом LOBBY_OPEN
    DB-->>S: session_id, status
    S-->>C: Множество доступных лобби
    C-->>U: Список доступных лобби
```

## 4. Вход в лобби

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    U->>C: Вход в лобби
    C->>S: Auth Token + session_id
    S->>DB: session_id + user_id
    Note over S,DB: Проверки — status = LOBBY_OPEN, игроков < 4,<br/>пользователь ещё не состоит в этом лобби
    DB-->>S: session_id, status, players[]
    S-->>C: Лобби
    C-->>U: Игрок входит в матч
```

## 5. Начало игры и первый бросок

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    U->>C: Начало игры
    C->>S: Auth Token + session_id
    S->>DB: SELECT status, players[], turn_amount
    DB-->>S: status, players[], turn_amount
    Note over S,DB: Сервер проверяет — 2..4 игрока, status = LOBBY_OPEN.<br/>Порядок игроков задаётся через Players.turn_at.
    S->>DB: UPDATE status = IN_PROGRESS
    DB-->>S: OK
    Note over S,DB: Автоматически, без ввода пользователя — первый бросок<br/>всеми 5 костями для игрока с первым turn_at.<br/>Значения 1..6 генерирует сервер, combo_id пока NULL.
    S->>DB: INSERT Turn + 5 Dice
    DB-->>S: turn_id, dice[]
    S-->>C: current_player_id, turn_number = 1, turn_id, throw_number = 1, dice[], combinations[]
    C-->>U: Игровая страница с результатом первого броска
```

## 6. Фиксация костей и повторный бросок

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    loop До нажатия «Бросить повторно»
        U->>C: Зафиксировать / освободить кость
        C-->>U: Кость отмечена (только на клиенте)
    end
    U->>C: Бросить повторно
    C->>S: Auth Token + turn_id + fixed_die_ids[]
    S->>DB: SELECT Dice, throw_number
    DB-->>S: dice[], throw_number
    Note over S,DB: Проверки — ход игрока и не закрыт, throw_number < 3,<br/>все die_id принадлежат ходу, зафиксировано 0..4 костей.
    S->>DB: UPDATE Dice.is_fixed + нефиксированных Dice + throw_number + 1
    Note over S,DB: Одна транзакция: сначала фиксация по fixed_die_ids[],<br/>затем переброс только is_fixed = FALSE. Максимум 3 броска за ход.
    DB-->>S: dice[], throw_number
    S-->>C: dice[], throw_number, combinations[]
    C-->>U: Результат броска
```

## 7. Выбор комбинации / завершение хода

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    U->>C: Выбрать комбинацию / завершить ход
    C->>S: Auth Token + turn_id + combo_id
    S->>DB: SELECT Dice + Combination
    Note over S,DB: Сервер проверяет, что combo_id соответствует текущим костям.<br/>При нескольких доступных комбинациях игрок выбирает одну.
    DB-->>S: dice[], Combinations.points
    S->>DB: UPDATE Turns.combo_id + Players.total_points
    Note over S,DB: score = сумма костей комбинации + Combinations.points.<br/>Комбинация с 1-го броска — x2, кроме «Шанса».<br/>После 3 бросков без комбинации score = 0.<br/>В Turns колонки score нет — очки вычисляются функцией turn_score().
    DB-->>S: score, total_points
    opt Не все игроки сделали N ходов
        Note over S,DB: Автоматический первый бросок для следующего игрока по Players.turn_at.
        S->>DB: INSERT Turn + 5 Dice
        DB-->>S: turn_id, dice[]
    end
    S-->>C: score, total_points
    C-->>U: Результат хода
```

## 8. Состояние партии (short-polling)

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    loop Short-polling, пока status != FINISHED
        C->>S: Auth Token + session_id
        S->>DB: SELECT status, players, completed turns, turn_amount, текущий Turn + Dice
        DB-->>S: Состояние партии
        alt Не все игроки сделали N ходов
            Note over S,DB: Текущий игрок определяется по Players.turn_at.<br/>Его первый бросок уже сделан автоматически — клиент получает кости.
            S-->>C: status = IN_PROGRESS, current_player_id, turn_number, turn_id, throw_number, dice[], combinations[], standings[]
            C-->>U: Следующий ход
        else У всех игроков N ходов
            opt status = IN_PROGRESS (первый запрос после последнего хода)
                S->>DB: UPDATE status = FINISHED
                DB-->>S: OK
            end
            S->>DB: SELECT players + total_points
            DB-->>S: results[]
            Note over S,DB: Максимальный total_points определяет победителя.<br/>При равенстве максимума winner_ids содержит несколько игроков (ничья).
            S-->>C: status = FINISHED, results[], winner_ids[]
            C-->>U: Страница результатов
        end
    end
```

## 9. Выход из матча

```mermaid
sequenceDiagram
    autonumber
    actor U as Пользователь
    participant C as Клиент
    participant S as Сервер
    participant DB as БД

    U->>C: Выход из матча
    C->>S: Auth Token + session_id
    S->>DB: session_id + user_id
    DB-->>S: user_id, login
    S-->>C: Профиль
    C-->>U: Профиль игрока
```

---

## Сводка данных

| Сценарий | Клиент → Сервер | Сервер → Клиент |
|---|---|---|
| Авторизация / регистрация | Логин, Пароль | Профиль + Auth Token, список лобби |
| Создание лобби | Auth Token + turn_amount | session_id, status |
| Поиск лобби | Auth Token | Множество лобби со status = LOBBY_OPEN |
| Вход в лобби | Auth Token + session_id | session_id, status, players[] |
| Начало игры и первый бросок | Auth Token + session_id | current_player_id, turn_number = 1, turn_id, throw_number = 1, dice[], combinations[] |
| Фиксация и повторный бросок | Auth Token + turn_id + fixed_die_ids[] | dice[], throw_number, combinations[] |
| Завершение хода | Auth Token + turn_id + combo_id | score, total_points |
| Состояние партии (polling) | Auth Token + session_id | `IN_PROGRESS`: current_player_id, turn_number, turn_id, throw_number, dice[], combinations[], standings[]<br/>`FINISHED`: results[], winner_ids[] |
| Выход из матча | Auth Token + session_id | Профиль игрока |
