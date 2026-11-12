package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Queries — обёртки над SQL-функциями. Имена методов повторяют имена функций.
type Queries struct {
	q Querier
}

// ---------- Модели ----------

type Die struct {
	DieID   int32 `json:"die_id"`
	Value   int32 `json:"value"`
	IsFixed bool  `json:"is_fixed"`
}

type UserCredentials struct {
	UserID       int32  `db:"user_id"`
	Login        string `db:"login"`
	PasswordHash string `db:"password_hash"`
}

type Profile struct {
	UserID      int32      `db:"user_id"      json:"user_id"`
	Login       string     `db:"login"        json:"login"`
	LastActive  *time.Time `db:"last_active"  json:"last_active"`
	GamesPlayed int32      `db:"games_played" json:"games_played"`
	GamesWon    int32      `db:"games_won"    json:"games_won"`
	BestTotal   int32      `db:"best_total"   json:"best_total"`
}

type LobbySummary struct {
	SessionID    int32     `db:"session_id"    json:"session_id"`
	TurnAmount   int32     `db:"turn_amount"   json:"turn_amount"`
	PlayersCount int32     `db:"players_count" json:"players_count"`
	StartedAt    time.Time `db:"started_at"    json:"started_at"`
}

type LobbyPlayer struct {
	PlayerID int32  `json:"player_id"`
	UserID   int32  `json:"user_id"`
	Login    string `json:"login"`
	TurnAt   int32  `json:"turn_at"`
}

type Lobby struct {
	SessionID  int32         `db:"session_id"  json:"session_id"`
	Status     string        `db:"status"      json:"status"`
	TurnAmount int32         `db:"turn_amount" json:"turn_amount"`
	Players    []LobbyPlayer `db:"players"     json:"players"`
}

type TurnStart struct {
	SessionID       int32 `db:"session_id"        json:"session_id"`
	CurrentPlayerID int32 `db:"current_player_id" json:"current_player_id"`
	TurnNumber      int32 `db:"turn_number"       json:"turn_number"`
	TurnID          int32 `db:"turn_id"           json:"turn_id"`
	ThrowNumber     int32 `db:"throw_number"      json:"throw_number"`
	Dice            []Die `db:"dice"              json:"dice"`
}

type Reroll struct {
	TurnID      int32 `db:"turn_id"      json:"turn_id"`
	ThrowNumber int32 `db:"throw_number" json:"throw_number"`
	Dice        []Die `db:"dice"         json:"dice"`
}

type TurnState struct {
	TurnID      int32  `db:"turn_id"      json:"turn_id"`
	TurnNumber  int32  `db:"turn_number"  json:"turn_number"`
	ThrowNumber int32  `db:"throw_number" json:"throw_number"`
	Dice        []Die  `db:"dice"         json:"dice"`
	ComboID     *int32 `db:"combo_id"     json:"combo_id"`
	Score       int32  `db:"score"        json:"score"`
}

type AvailableCombination struct {
	ComboID        int32  `db:"combo_id"        json:"combo_id"`
	Name           string `db:"name"            json:"name"`
	Points         int32  `db:"points"          json:"points"`
	PotentialScore int32  `db:"potential_score" json:"potential_score"`
}

type Combination struct {
	ComboID int32  `db:"combo_id" json:"combo_id"`
	Name    string `db:"name"     json:"name"`
	Points  int32  `db:"points"   json:"points"`
}

type TurnResult struct {
	Score       int32 `db:"score"        json:"score"`
	TotalPoints int32 `db:"total_points" json:"total_points"`
}

type Standing struct {
	PlayerID    int32  `json:"player_id"`
	UserID      int32  `json:"user_id"`
	Login       string `json:"login"`
	TotalPoints int32  `json:"total_points"`
	TurnsPlayed int32  `json:"turns_played"`
	Place       int32  `json:"place"`
}

type TurnProgress struct {
	CurrentPlayerID int32      `db:"current_player_id"`
	TurnNumber      int32      `db:"turn_number"`
	Standings       []Standing `db:"standings"`
}

type SessionResult struct {
	Status    string     `db:"status"     json:"status"`
	Results   []Standing `db:"results"    json:"results"`
	WinnerIDs []int32    `db:"winner_ids" json:"winner_ids"`
}

// ---------- Хелперы ----------

func one[T any](ctx context.Context, q Querier, sql string, args ...any) (T, error) {
	rows, _ := q.Query(ctx, sql, args...)
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[T])
}

func many[T any](ctx context.Context, q Querier, sql string, args ...any) ([]T, error) {
	rows, _ := q.Query(ctx, sql, args...)
	res, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if res == nil && err == nil {
		res = []T{}
	}
	return res, err
}

func scalar[T any](ctx context.Context, q Querier, sql string, args ...any) (T, error) {
	var v T
	err := q.QueryRow(ctx, sql, args...).Scan(&v)
	return v, err
}

// ---------- 1. Пользователи и вход ----------

func (q *Queries) RegisterUser(ctx context.Context, login, passwordHash string) (int32, error) {
	return scalar[int32](ctx, q.q, `SELECT register_user($1, $2)`, login, passwordHash)
}

// FindUserByLogin возвращает nil, если пользователя нет.
func (q *Queries) FindUserByLogin(ctx context.Context, login string) (*UserCredentials, error) {
	u, err := one[UserCredentials](ctx, q.q, `SELECT * FROM find_user_by_login($1)`, login)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (q *Queries) TouchLastActive(ctx context.Context, userID int32) error {
	_, err := q.q.Exec(ctx, `SELECT touch_last_active($1)`, userID)
	return err
}

func (q *Queries) GetProfile(ctx context.Context, userID int32) (Profile, error) {
	return one[Profile](ctx, q.q, `SELECT * FROM get_profile($1)`, userID)
}

// ---------- 2. Лобби ----------

func (q *Queries) CreateLobby(ctx context.Context, userID, turnAmount int32) (int32, error) {
	return scalar[int32](ctx, q.q, `SELECT create_lobby($1, $2)`, userID, turnAmount)
}

func (q *Queries) ListOpenLobbies(ctx context.Context, limit, offset int32) ([]LobbySummary, error) {
	return many[LobbySummary](ctx, q.q, `SELECT * FROM list_open_lobbies($1, $2)`, limit, offset)
}

func (q *Queries) GetLobby(ctx context.Context, sessionID int32) (Lobby, error) {
	l, err := one[Lobby](ctx, q.q, `SELECT * FROM get_lobby($1)`, sessionID)
	if l.Players == nil {
		l.Players = []LobbyPlayer{}
	}
	return l, err
}

func (q *Queries) JoinLobby(ctx context.Context, sessionID, userID int32) (int32, error) {
	return scalar[int32](ctx, q.q, `SELECT join_lobby($1, $2)`, sessionID, userID)
}

func (q *Queries) LeaveLobby(ctx context.Context, sessionID, userID int32) error {
	_, err := q.q.Exec(ctx, `SELECT leave_lobby($1, $2)`, sessionID, userID)
	return err
}

func (q *Queries) StartGame(ctx context.Context, sessionID, userID int32) (TurnStart, error) {
	return one[TurnStart](ctx, q.q, `SELECT * FROM start_game($1, $2)`, sessionID, userID)
}

func (q *Queries) LeaveMatch(ctx context.Context, sessionID, userID int32) error {
	_, err := q.q.Exec(ctx, `SELECT * FROM leave_match($1, $2)`, sessionID, userID)
	return err
}

// ---------- 3. Ход ----------

func (q *Queries) ActiveTurn(ctx context.Context, userID int32) (int32, error) {
	return scalar[int32](ctx, q.q, `SELECT active_turn($1)`, userID)
}

func (q *Queries) IsTurnClosed(ctx context.Context, turnID int32) (bool, error) {
	return scalar[bool](ctx, q.q, `SELECT is_turn_closed($1)`, turnID)
}

func (q *Queries) RerollDice(ctx context.Context, userID int32, fixedDieIDs []int32) (Reroll, error) {
	if fixedDieIDs == nil {
		fixedDieIDs = []int32{}
	}
	return one[Reroll](ctx, q.q, `SELECT * FROM reroll_dice($1, $2)`, userID, fixedDieIDs)
}

func (q *Queries) GetTurnState(ctx context.Context, turnID int32) (TurnState, error) {
	return one[TurnState](ctx, q.q, `SELECT * FROM get_turn_state($1)`, turnID)
}

func (q *Queries) AvailableCombinations(ctx context.Context, turnID int32) ([]AvailableCombination, error) {
	return many[AvailableCombination](ctx, q.q, `SELECT * FROM available_combinations($1)`, turnID)
}

func (q *Queries) FinishTurn(ctx context.Context, turnID, comboID int32) (TurnResult, error) {
	return one[TurnResult](ctx, q.q, `SELECT * FROM finish_turn($1, $2)`, turnID, comboID)
}

func (q *Queries) SkipTurn(ctx context.Context, turnID int32) (TurnResult, error) {
	return one[TurnResult](ctx, q.q, `SELECT * FROM skip_turn($1)`, turnID)
}

func (q *Queries) AdvanceTurn(ctx context.Context, sessionID int32) (TurnProgress, error) {
	p, err := one[TurnProgress](ctx, q.q, `SELECT * FROM advance_turn($1)`, sessionID)
	if p.Standings == nil {
		p.Standings = []Standing{}
	}
	return p, err
}

// ---------- 4. Справочник комбинаций ----------

func (q *Queries) ListCombinations(ctx context.Context) ([]Combination, error) {
	return many[Combination](ctx, q.q, `SELECT combo_id, name, points FROM combinations ORDER BY combo_id`)
}

// ---------- 5. Завершение партии ----------

func (q *Queries) IsSessionComplete(ctx context.Context, sessionID int32) (bool, error) {
	return scalar[bool](ctx, q.q, `SELECT is_session_complete($1)`, sessionID)
}

func (q *Queries) FinishSession(ctx context.Context, sessionID int32) (SessionResult, error) {
	return normalizeResult(one[SessionResult](ctx, q.q, `SELECT * FROM finish_session($1)`, sessionID))
}

func (q *Queries) GetSessionResult(ctx context.Context, sessionID int32) (SessionResult, error) {
	return normalizeResult(one[SessionResult](ctx, q.q, `SELECT * FROM get_session_result($1)`, sessionID))
}

func normalizeResult(r SessionResult, err error) (SessionResult, error) {
	if r.Results == nil {
		r.Results = []Standing{}
	}
	if r.WinnerIDs == nil {
		r.WinnerIDs = []int32{}
	}
	return r, err
}
