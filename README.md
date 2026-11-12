# databases-itmo — «Покер на костях»

Сервер игры на Go поверх PostgreSQL. Вся игровая логика и валидация находится в SQL-функциях,
сервер проверяет Auth Token, хеширует пароли и вызывает функции.

Документация:

* [`docs/game-rules.md`](docs/game-rules.md) — правила игры как требования к БД
* [`docs/api-schema.md`](docs/api-schema.md) — сценарии взаимодействия клиент ↔ сервер ↔ БД
* [`docs/sql-functions.md`](docs/sql-functions.md) — схема, SQL-функции, триггеры, коды ошибок

## Запуск

```sh
make up        # PostgreSQL + сервер в Docker
```

Swagger UI: <http://localhost:8080/swagger/index.html>, спецификация: `/openapi.yaml`.

Локально без Docker-образа сервера:

```sh
make db        # только PostgreSQL в Docker
make run       # go run ./cmd/server, миграции применяются при старте
```

| Переменная | По умолчанию |
|---|---|
| `DATABASE_URL` | `postgres://poker:poker@localhost:5432/poker?sslmode=disable` |
| `HTTP_ADDR` | `:8080` |
| `JWT_SECRET` | `dev-secret-change-me` (с предупреждением в логе) |
| `TOKEN_TTL` | `24h` |
| `MIGRATE` | `true` — применять `db/migrations` при старте |

## Как тестировать партию в Swagger

1. `POST /api/auth/register` для двух пользователей (`alice`, `bob`) и сохраните оба `token`.
2. **Authorize** → токен `alice` → `POST /api/lobbies` с `{"turn_amount": 2}`.
3. **Authorize** → токен `bob` → `POST /api/lobbies/{session_id}/join`, затем `.../start`.
4. `GET /api/sessions/{session_id}/state` покажет `current_player_id` и кости. В `players` найдите,
   чей это `player_id`, и переключите токен на этого игрока.
5. `POST /api/game/reroll` с `{"fixed_die_ids": [...]}` (до двух раз), затем
   `POST /api/turns/{turn_id}/finish` с `combo_id` из `combinations`.
6. Повторяйте шаги 4–5, пока `state` не вернёт `FINISHED`, `results` и `winner_ids`.

## Эндпоинты → SQL-функции

| Сценарий | Метод и путь | Функции |
|---|---|---|
| 1. Регистрация | `POST /api/auth/register` | `register_user`, `touch_last_active`, `get_profile`, `list_open_lobbies` |
| 1. Вход | `POST /api/auth/login` | `find_user_by_login`, `touch_last_active`, `get_profile`, `list_open_lobbies` |
| Профиль | `GET /api/profile` | `get_profile` |
| Справочник | `GET /api/combinations` | таблица `Combinations` |
| 2. Создание лобби | `POST /api/lobbies` | `create_lobby`, `get_lobby` |
| 3. Поиск лобби | `GET /api/lobbies` | `list_open_lobbies` |
| Карточка лобби | `GET /api/lobbies/{id}` | `get_lobby` |
| 4. Вход в лобби | `POST /api/lobbies/{id}/join` | `join_lobby`, `get_lobby` |
| Выход из лобби | `POST /api/lobbies/{id}/leave` | `leave_lobby` |
| 5. Старт и первый бросок | `POST /api/lobbies/{id}/start` | `start_game`, `available_combinations` |
| 6. Переброс | `POST /api/game/reroll` | `reroll_dice`, `available_combinations` |
| Состояние хода | `GET /api/turns/{id}` | `get_turn_state`, `is_turn_closed`, `available_combinations` |
| 7. Выбор комбинации | `POST /api/turns/{id}/finish` | `active_turn`, `finish_turn` |
| 7. Ход без комбинации | `POST /api/turns/{id}/skip` | `active_turn`, `skip_turn` |
| 8. Состояние партии | `GET /api/sessions/{id}/state` | `get_lobby`, `is_session_complete`, `advance_turn`, `active_turn`, `get_turn_state`, `available_combinations`, `finish_session`, `get_session_result` |
| 9. Выход из матча | `POST /api/sessions/{id}/leave` | `leave_match`, `get_profile` |

Каждый запрос с токеном вызывает `touch_last_active`. Ошибки приходят как
`{"error": "<константа>", "message": "..."}`, HTTP-статусы взяты из таблицы
[«Коды ошибок»](docs/sql-functions.md#коды-ошибок).

## Структура

```
cmd/server/          точка входа: конфиг, подключение к БД, миграции, HTTP
db/migrations/       SQL из docs/sql-functions.md: схема, подсчёт очков, функции, триггеры, справочник
internal/store/      вызовы SQL-функций, транзакции, раннер миграций
internal/httpapi/    маршруты, авторизация, маппинг ошибок, интеграционные тесты
internal/auth/       JWT (HS256) и bcrypt
api/openapi.yaml     OpenAPI 3 для Swagger UI
```

## Тесты

Интеграционные тесты проходят всю партию через HTTP. Каждый тест создаёт отдельную БД в кластере
`TEST_DATABASE_URL` и удаляет её после себя.

```sh
make db && make test
```
