package httpapi

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/seventwogth/databases-itmo/internal/auth"
	"github.com/seventwogth/databases-itmo/internal/store"
)

const lobbiesOnLogin = 50

// ---------- 1. Авторизация / регистрация ----------

type credentialsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type authResponse struct {
	Token     string               `json:"token"`
	ExpiresAt time.Time            `json:"expires_at"`
	Profile   store.Profile        `json:"profile"`
	Lobbies   []store.LobbySummary `json:"lobbies"`
}

func (req *credentialsRequest) validate() error {
	req.Login = strings.TrimSpace(req.Login)
	if req.Login == "" || utf8.RuneCountInString(req.Login) > 64 {
		return errBadRequest("login must be 1..64 characters")
	}
	if req.Password == "" || len(req.Password) > 72 { // bcrypt учитывает только первые 72 байта
		return errBadRequest("password must be 1..72 bytes")
	}
	return nil
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	var req credentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return err
	}
	var resp authResponse
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		uid, err := q.RegisterUser(r.Context(), req.Login, hash)
		if err != nil {
			return err
		}
		if err := q.TouchLastActive(r.Context(), uid); err != nil {
			return err
		}
		return s.fillAuthResponse(r, q, uid, &resp)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var req credentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return errInvalidCredentials
	}
	var resp authResponse
	err := s.store.InTx(r.Context(), func(q *store.Queries) error {
		u, err := q.FindUserByLogin(r.Context(), req.Login)
		if err != nil {
			return err
		}
		// пароль сверяет сервер — БД отдаёт только хеш
		if u == nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
			return errInvalidCredentials
		}
		if err := q.TouchLastActive(r.Context(), u.UserID); err != nil {
			return err
		}
		return s.fillAuthResponse(r, q, u.UserID, &resp)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) fillAuthResponse(r *http.Request, q *store.Queries, uid int32, resp *authResponse) error {
	var err error
	if resp.Profile, err = q.GetProfile(r.Context(), uid); err != nil {
		return err
	}
	if resp.Lobbies, err = q.ListOpenLobbies(r.Context(), lobbiesOnLogin, 0); err != nil {
		return err
	}
	resp.Token, resp.ExpiresAt, err = s.tokens.Issue(uid)
	return err
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Q().GetProfile(r.Context(), userID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}

func (s *Server) combinations(w http.ResponseWriter, r *http.Request) error {
	c, err := s.store.Q().ListCombinations(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, c)
	return nil
}

// ---------- 2–4. Лобби ----------

type createLobbyRequest struct {
	TurnAmount int32 `json:"turn_amount"`
}

func (s *Server) createLobby(w http.ResponseWriter, r *http.Request) error {
	var req createLobbyRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if req.TurnAmount < 1 {
		return errBadRequest("turn_amount must be >= 1")
	}
	var lobby store.Lobby
	err := s.store.InTx(r.Context(), func(q *store.Queries) error {
		sid, err := q.CreateLobby(r.Context(), userID(r.Context()), req.TurnAmount)
		if err != nil {
			return err
		}
		lobby, err = q.GetLobby(r.Context(), sid)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, lobby)
	return nil
}

func (s *Server) listLobbies(w http.ResponseWriter, r *http.Request) error {
	limit, err := queryInt(r, "limit", 50, 1, 200)
	if err != nil {
		return err
	}
	offset, err := queryInt(r, "offset", 0, 0, 1<<30)
	if err != nil {
		return err
	}
	lobbies, err := s.store.Q().ListOpenLobbies(r.Context(), limit, offset)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, lobbies)
	return nil
}

func (s *Server) getLobby(w http.ResponseWriter, r *http.Request) error {
	sid, err := pathID(r, "session_id")
	if err != nil {
		return err
	}
	lobby, err := s.store.Q().GetLobby(r.Context(), sid)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, lobby)
	return nil
}

func (s *Server) joinLobby(w http.ResponseWriter, r *http.Request) error {
	sid, err := pathID(r, "session_id")
	if err != nil {
		return err
	}
	var lobby store.Lobby
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		if _, err := q.JoinLobby(r.Context(), sid, userID(r.Context())); err != nil {
			return err
		}
		lobby, err = q.GetLobby(r.Context(), sid)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, lobby)
	return nil
}

func (s *Server) leaveLobby(w http.ResponseWriter, r *http.Request) error {
	sid, err := pathID(r, "session_id")
	if err != nil {
		return err
	}
	if err := s.store.Q().LeaveLobby(r.Context(), sid, userID(r.Context())); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- 5. Начало игры и первый бросок ----------

type startGameResponse struct {
	store.TurnStart
	Combinations []store.AvailableCombination `json:"combinations"`
}

func (s *Server) startGame(w http.ResponseWriter, r *http.Request) error {
	sid, err := pathID(r, "session_id")
	if err != nil {
		return err
	}
	var resp startGameResponse
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		if resp.TurnStart, err = q.StartGame(r.Context(), sid, userID(r.Context())); err != nil {
			return err
		}
		resp.Combinations, err = q.AvailableCombinations(r.Context(), resp.TurnID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// ---------- 6. Фиксация костей и повторный бросок ----------

type rerollRequest struct {
	FixedDieIDs []int32 `json:"fixed_die_ids"`
}

type rerollResponse struct {
	store.Reroll
	Combinations []store.AvailableCombination `json:"combinations"`
}

func (s *Server) reroll(w http.ResponseWriter, r *http.Request) error {
	var req rerollRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	var resp rerollResponse
	err := s.store.InTx(r.Context(), func(q *store.Queries) error {
		var err error
		if resp.Reroll, err = q.RerollDice(r.Context(), userID(r.Context()), req.FixedDieIDs); err != nil {
			return err
		}
		resp.Combinations, err = q.AvailableCombinations(r.Context(), resp.TurnID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// ---------- 7. Выбор комбинации / завершение хода ----------

type turnStateResponse struct {
	store.TurnState
	IsClosed     bool                         `json:"is_closed"`
	Combinations []store.AvailableCombination `json:"combinations,omitzero"`
}

func (s *Server) getTurn(w http.ResponseWriter, r *http.Request) error {
	tid, err := pathID(r, "turn_id")
	if err != nil {
		return err
	}
	var resp turnStateResponse
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		if resp.TurnState, err = q.GetTurnState(r.Context(), tid); err != nil {
			return err
		}
		if resp.IsClosed, err = q.IsTurnClosed(r.Context(), tid); err != nil || resp.IsClosed {
			return err
		}
		resp.Combinations, err = q.AvailableCombinations(r.Context(), tid)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type finishTurnRequest struct {
	ComboID int32 `json:"combo_id"`
}

// ensureOwnTurn: finish_turn / skip_turn проверяют, что ход текущего игрока,
// но не что его закрывает именно этот пользователь — это проверка сервера.
func ensureOwnTurn(r *http.Request, q *store.Queries, turnID int32) error {
	active, err := q.ActiveTurn(r.Context(), userID(r.Context()))
	if err != nil {
		return err
	}
	if active != turnID {
		return errNotYourTurn
	}
	return nil
}

func (s *Server) finishTurn(w http.ResponseWriter, r *http.Request) error {
	tid, err := pathID(r, "turn_id")
	if err != nil {
		return err
	}
	var req finishTurnRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if req.ComboID <= 0 {
		return errBadRequest("combo_id must be a positive integer")
	}
	var res store.TurnResult
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		if err := ensureOwnTurn(r, q, tid); err != nil {
			return err
		}
		res, err = q.FinishTurn(r.Context(), tid, req.ComboID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) skipTurn(w http.ResponseWriter, r *http.Request) error {
	tid, err := pathID(r, "turn_id")
	if err != nil {
		return err
	}
	var res store.TurnResult
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		if err := ensureOwnTurn(r, q, tid); err != nil {
			return err
		}
		res, err = q.SkipTurn(r.Context(), tid)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// ---------- 8. Состояние партии (short-polling) ----------

type sessionStateResponse struct {
	SessionID  int32               `json:"session_id"`
	Status     string              `json:"status"`
	TurnAmount int32               `json:"turn_amount"`
	Players    []store.LobbyPlayer `json:"players"`

	// IN_PROGRESS
	CurrentPlayerID *int32                       `json:"current_player_id,omitempty"`
	TurnNumber      *int32                       `json:"turn_number,omitempty"`
	TurnID          *int32                       `json:"turn_id,omitempty"`
	ThrowNumber     *int32                       `json:"throw_number,omitempty"`
	Dice            []store.Die                  `json:"dice,omitzero"`
	Combinations    []store.AvailableCombination `json:"combinations,omitzero"`
	Standings       []store.Standing             `json:"standings,omitzero"`

	// FINISHED
	Results   []store.Standing `json:"results,omitzero"`
	WinnerIDs []int32          `json:"winner_ids,omitzero"`
}

func (s *Server) sessionState(w http.ResponseWriter, r *http.Request) error {
	sid, err := pathID(r, "session_id")
	if err != nil {
		return err
	}
	var resp sessionStateResponse
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		ctx := r.Context()
		lobby, err := q.GetLobby(ctx, sid)
		if err != nil {
			return err
		}
		resp.SessionID, resp.Status, resp.TurnAmount, resp.Players = lobby.SessionID, lobby.Status, lobby.TurnAmount, lobby.Players

		switch lobby.Status {
		case "LOBBY_OPEN":
			return nil
		case "IN_PROGRESS":
			complete, err := q.IsSessionComplete(ctx, sid)
			if err != nil {
				return err
			}
			if complete {
				// первый запрос после последнего хода — завершаем партию
				res, err := q.FinishSession(ctx, sid)
				if err != nil {
					return err
				}
				resp.Status, resp.Results, resp.WinnerIDs = res.Status, res.Results, res.WinnerIDs
				return nil
			}
			return fillInProgress(r, q, sid, &resp)
		default: // FINISHED
			res, err := q.GetSessionResult(ctx, sid)
			if err != nil {
				return err
			}
			resp.Results, resp.WinnerIDs = res.Results, res.WinnerIDs
			return nil
		}
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func fillInProgress(r *http.Request, q *store.Queries, sid int32, resp *sessionStateResponse) error {
	ctx := r.Context()
	progress, err := q.AdvanceTurn(ctx, sid)
	if err != nil {
		return err
	}
	resp.CurrentPlayerID, resp.TurnNumber, resp.Standings = &progress.CurrentPlayerID, &progress.TurnNumber, progress.Standings

	var currentUser int32
	for _, st := range progress.Standings {
		if st.PlayerID == progress.CurrentPlayerID {
			currentUser = st.UserID
		}
	}
	// первый бросок текущего игрока уже сделан автоматически — отдаём его кости
	turnID, err := q.ActiveTurn(ctx, currentUser)
	if err != nil {
		return err
	}
	turn, err := q.GetTurnState(ctx, turnID)
	if err != nil {
		return err
	}
	resp.TurnID, resp.ThrowNumber, resp.Dice = &turn.TurnID, &turn.ThrowNumber, turn.Dice
	resp.Combinations, err = q.AvailableCombinations(ctx, turnID)
	return err
}

// ---------- 9. Выход из матча ----------

func (s *Server) leaveMatch(w http.ResponseWriter, r *http.Request) error {
	sid, err := pathID(r, "session_id")
	if err != nil {
		return err
	}
	uid := userID(r.Context())
	var p store.Profile
	err = s.store.InTx(r.Context(), func(q *store.Queries) error {
		if err := q.LeaveMatch(r.Context(), sid, uid); err != nil {
			return err
		}
		p, err = q.GetProfile(r.Context(), uid)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, p)
	return nil
}
