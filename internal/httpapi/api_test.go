package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/seventwogth/databases-itmo/db"
	"github.com/seventwogth/databases-itmo/internal/auth"
	"github.com/seventwogth/databases-itmo/internal/httpapi"
	"github.com/seventwogth/databases-itmo/internal/store"
)

// newTestServer создаёт отдельную БД в кластере TEST_DATABASE_URL, применяет миграции
// и поднимает API. Без TEST_DATABASE_URL тесты пропускаются.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	adminURL := os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("TEST_DATABASE_URL не задан")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	dbName := fmt.Sprintf("poker_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(adminURL)
	u.Path = "/" + dbName
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		_ = admin.Close(ctx)
	})
	if _, err := store.Migrate(ctx, pool, db.Migrations, "migrations"); err != nil {
		t.Fatal(err)
	}
	// повторный запуск миграций ничего не применяет
	if again, err := store.Migrate(ctx, pool, db.Migrations, "migrations"); err != nil || len(again) != 0 {
		t.Fatalf("re-migrate: applied=%v err=%v", again, err)
	}
	srv := httptest.NewServer(httpapi.New(store.New(pool), auth.NewTokens("test", time.Hour)).Handler())
	t.Cleanup(srv.Close)
	return srv
}

type client struct {
	t   *testing.T
	srv *httptest.Server
}

// call выполняет запрос, проверяет статус и раскладывает JSON-ответ в out.
func (c client) call(method, path, token string, body any, wantStatus int, out any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		c.t.Fatalf("%s %s: status %d, want %d; body: %s", method, path, resp.StatusCode, wantStatus, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
}

func (c client) expectError(method, path, token string, body any, wantStatus int, wantCode string) {
	c.t.Helper()
	var e struct{ Error string }
	c.call(method, path, token, body, wantStatus, &e)
	if e.Error != wantCode {
		c.t.Fatalf("%s %s: error %q, want %q", method, path, e.Error, wantCode)
	}
}

type authResp struct {
	Token   string
	Profile struct {
		UserID int32 `json:"user_id"`
	}
}

type die struct {
	DieID   int32 `json:"die_id"`
	Value   int32
	IsFixed bool `json:"is_fixed"`
}

type combo struct {
	ComboID        int32 `json:"combo_id"`
	Name           string
	PotentialScore int32 `json:"potential_score"`
}

type playerRef struct {
	PlayerID    int32 `json:"player_id"`
	UserID      int32 `json:"user_id"`
	TotalPoints int32 `json:"total_points"`
}

type state struct {
	Status          string
	Players         []playerRef
	CurrentPlayerID int32 `json:"current_player_id"`
	TurnID          int32 `json:"turn_id"`
	ThrowNumber     int32 `json:"throw_number"`
	Dice            []die
	Combinations    []combo
	Results         []playerRef
	WinnerIDs       []int32 `json:"winner_ids"`
}

func TestFullGame(t *testing.T) {
	c := client{t: t, srv: newTestServer(t)}

	// 1. Регистрация и вход
	var alice, bob authResp
	c.call("POST", "/api/auth/register", "", map[string]string{"login": "alice", "password": "pw-a"}, 201, &alice)
	c.call("POST", "/api/auth/register", "", map[string]string{"login": "bob", "password": "pw-b"}, 201, &bob)
	c.expectError("POST", "/api/auth/register", "", map[string]string{"login": "alice", "password": "x"}, 409, "login_already_taken")
	c.expectError("POST", "/api/auth/login", "", map[string]string{"login": "alice", "password": "wrong"}, 401, "invalid_credentials")
	c.call("POST", "/api/auth/login", "", map[string]string{"login": "alice", "password": "pw-a"}, 200, &alice)
	c.expectError("GET", "/api/profile", "", nil, 401, "unauthorized")
	c.expectError("GET", "/api/profile", "garbage", nil, 401, "unauthorized")

	var combos []map[string]any
	c.call("GET", "/api/combinations", alice.Token, nil, 200, &combos)
	if len(combos) != 11 {
		t.Fatalf("combinations: %d, want 11", len(combos))
	}

	// 2–4. Лобби
	c.expectError("POST", "/api/lobbies", alice.Token, map[string]int{"turn_amount": 0}, 400, "invalid_argument")
	var lobby struct {
		SessionID int32 `json:"session_id"`
		Status    string
		Players   []any
	}
	c.call("POST", "/api/lobbies", alice.Token, map[string]int{"turn_amount": 2}, 201, &lobby)
	if lobby.Status != "LOBBY_OPEN" || len(lobby.Players) != 1 {
		t.Fatalf("lobby after create: %+v", lobby)
	}
	sid := lobby.SessionID
	base := fmt.Sprintf("/api/lobbies/%d", sid)
	c.expectError("POST", "/api/lobbies", alice.Token, map[string]int{"turn_amount": 2}, 409, "already_in_session")
	c.expectError("POST", base+"/start", alice.Token, nil, 409, "not_enough_players")

	var open []struct {
		SessionID int32 `json:"session_id"`
	}
	c.call("GET", "/api/lobbies", bob.Token, nil, 200, &open)
	if len(open) != 1 || open[0].SessionID != sid {
		t.Fatalf("open lobbies: %+v", open)
	}
	c.call("POST", base+"/join", bob.Token, nil, 200, &lobby)
	if len(lobby.Players) != 2 {
		t.Fatalf("lobby after join: %+v", lobby)
	}
	c.expectError("POST", base+"/join", bob.Token, nil, 409, "already_joined")
	c.expectError("POST", "/api/lobbies/999999/join", bob.Token, nil, 404, "lobby_not_found")

	var st state
	c.call("GET", fmt.Sprintf("/api/sessions/%d/state", sid), alice.Token, nil, 200, &st)
	if st.Status != "LOBBY_OPEN" {
		t.Fatalf("state: %+v", st)
	}

	// 5. Старт и первый бросок
	var start struct {
		TurnID       int32 `json:"turn_id"`
		ThrowNumber  int32 `json:"throw_number"`
		TurnNumber   int32 `json:"turn_number"`
		Dice         []die
		Combinations []combo
	}
	c.call("POST", base+"/start", bob.Token, nil, 200, &start)
	if len(start.Dice) != 5 || start.ThrowNumber != 1 || start.TurnNumber != 1 {
		t.Fatalf("start: %+v", start)
	}
	c.expectError("POST", base+"/join", bob.Token, nil, 409, "lobby_not_open")

	tokenByUser := map[int32]string{alice.Profile.UserID: alice.Token, bob.Profile.UserID: bob.Token}

	// 6–7. Партия: 2 игрока × 2 хода
	statePath := fmt.Sprintf("/api/sessions/%d/state", sid)
	for turn := 0; ; turn++ {
		if turn > 10 {
			t.Fatal("game did not finish")
		}
		c.call("GET", statePath, alice.Token, nil, 200, &st)
		if st.Status == "FINISHED" {
			break
		}
		if len(st.Dice) != 5 || st.ThrowNumber != 1 {
			t.Fatalf("turn %d state: %+v", turn, st)
		}
		var cur, other string
		for _, p := range st.Players {
			if p.PlayerID == st.CurrentPlayerID {
				cur = tokenByUser[p.UserID]
			} else {
				other = tokenByUser[p.UserID]
			}
		}

		// чужой ход трогать нельзя
		c.expectError("POST", "/api/game/reroll", other, map[string]any{"fixed_die_ids": []int32{}}, 403, "not_your_turn")
		c.expectError("POST", fmt.Sprintf("/api/turns/%d/finish", st.TurnID), other, map[string]int32{"combo_id": 1}, 403, "not_your_turn")
		// skip до третьего броска запрещён
		c.expectError("POST", fmt.Sprintf("/api/turns/%d/skip", st.TurnID), cur, nil, 409, "throws_not_exhausted")
		// чужая кость в фиксации
		c.expectError("POST", "/api/game/reroll", cur, map[string]any{"fixed_die_ids": []int32{999999}}, 400, "invalid_argument")

		// бросок 2: фиксируем первую кость — её значение не меняется
		fixed := st.Dice[0]
		var rr struct {
			ThrowNumber  int32 `json:"throw_number"`
			Dice         []die
			Combinations []combo
		}
		c.call("POST", "/api/game/reroll", cur, map[string]any{"fixed_die_ids": []int32{fixed.DieID}}, 200, &rr)
		if rr.ThrowNumber != 2 || rr.Dice[0].Value != fixed.Value || !rr.Dice[0].IsFixed || rr.Dice[1].IsFixed {
			t.Fatalf("reroll 2: %+v (fixed %+v)", rr, fixed)
		}
		// бросок 3: все свободны
		c.call("POST", "/api/game/reroll", cur, map[string]any{"fixed_die_ids": []int32{}}, 200, &rr)
		if rr.ThrowNumber != 3 {
			t.Fatalf("reroll 3: %+v", rr)
		}
		c.expectError("POST", "/api/game/reroll", cur, map[string]any{"fixed_die_ids": []int32{}}, 409, "no_throws_left")

		var res struct {
			Score       int32
			TotalPoints int32 `json:"total_points"`
		}
		finishPath := fmt.Sprintf("/api/turns/%d/finish", st.TurnID)
		// Любые 5 костей дают «Пару» или «Шанс», поэтому пропуск хода проверяем явно на первом ходу.
		if turn == 0 || len(rr.Combinations) == 0 {
			c.call("POST", fmt.Sprintf("/api/turns/%d/skip", st.TurnID), cur, nil, 200, &res)
			if res.Score != 0 {
				t.Fatalf("skip score: %+v", res)
			}
		} else {
			best := rr.Combinations[0]
			c.call("POST", finishPath, cur, map[string]int32{"combo_id": best.ComboID}, 200, &res)
			if res.Score != best.PotentialScore {
				t.Fatalf("score %d, potential %d", res.Score, best.PotentialScore)
			}
		}
		c.expectError("POST", finishPath, cur, map[string]int32{"combo_id": 1}, 403, "not_your_turn")

		var closed struct {
			IsClosed     bool `json:"is_closed"`
			Combinations []combo
		}
		c.call("GET", fmt.Sprintf("/api/turns/%d", st.TurnID), cur, nil, 200, &closed)
		if !closed.IsClosed || closed.Combinations != nil {
			t.Fatalf("closed turn: %+v", closed)
		}
	}

	// 8. Результаты
	if len(st.Results) != 2 || len(st.WinnerIDs) == 0 {
		t.Fatalf("finished state: %+v", st)
	}
	maxPts := st.Results[0].TotalPoints
	for _, w := range st.WinnerIDs {
		for _, r := range st.Results {
			if r.PlayerID == w && r.TotalPoints != maxPts {
				t.Fatalf("winner %d has %d, max %d", w, r.TotalPoints, maxPts)
			}
		}
	}
	c.expectError("POST", "/api/game/reroll", alice.Token, map[string]any{"fixed_die_ids": []int32{}}, 403, "not_your_turn")
	c.expectError("POST", fmt.Sprintf("/api/sessions/%d/leave", sid), alice.Token, nil, 409, "session_finished")

	var prof struct {
		GamesPlayed int32 `json:"games_played"`
	}
	c.call("GET", "/api/profile", alice.Token, nil, 200, &prof)
	if prof.GamesPlayed != 1 {
		t.Fatalf("profile: %+v", prof)
	}
}

func TestLeave(t *testing.T) {
	c := client{t: t, srv: newTestServer(t)}
	var a, b authResp
	c.call("POST", "/api/auth/register", "", map[string]string{"login": "a", "password": "p"}, 201, &a)
	c.call("POST", "/api/auth/register", "", map[string]string{"login": "b", "password": "p"}, 201, &b)

	// выход из лобби до старта; последний вышедший удаляет лобби
	var lobby struct {
		SessionID int32 `json:"session_id"`
	}
	c.call("POST", "/api/lobbies", a.Token, map[string]int{"turn_amount": 1}, 201, &lobby)
	base := fmt.Sprintf("/api/lobbies/%d", lobby.SessionID)
	c.call("POST", base+"/join", b.Token, nil, 200, nil)
	c.call("POST", base+"/leave", b.Token, nil, 204, nil)
	c.expectError("POST", base+"/leave", b.Token, nil, 404, "not_in_lobby")
	c.call("POST", base+"/leave", a.Token, nil, 204, nil)
	c.expectError("GET", base, a.Token, nil, 404, "lobby_not_found")

	// выход из матча: оставшийся игрок доигрывает один
	c.call("POST", "/api/lobbies", a.Token, map[string]int{"turn_amount": 1}, 201, &lobby)
	base = fmt.Sprintf("/api/lobbies/%d", lobby.SessionID)
	c.call("POST", base+"/join", b.Token, nil, 200, nil)
	c.call("POST", base+"/start", a.Token, nil, 200, nil)
	var prof struct{ Login string }
	c.call("POST", fmt.Sprintf("/api/sessions/%d/leave", lobby.SessionID), b.Token, nil, 200, &prof)
	if prof.Login != "b" {
		t.Fatalf("leave_match profile: %+v", prof)
	}
	var st state
	c.call("GET", fmt.Sprintf("/api/sessions/%d/state", lobby.SessionID), a.Token, nil, 200, &st)
	if st.Status != "IN_PROGRESS" || len(st.Players) != 1 || len(st.Dice) != 5 {
		t.Fatalf("state after leave: %+v", st)
	}
}
