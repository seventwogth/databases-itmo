package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// APIError — ответ об ошибке: {"error": "<константа>", "message": "..."}.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func newError(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Message: msg}
}

var (
	errUnauthorized       = newError(http.StatusUnauthorized, "unauthorized", "missing or invalid Auth Token")
	errInvalidCredentials = newError(http.StatusUnauthorized, "invalid_credentials", "wrong login or password")
	errNotYourTurn        = newError(http.StatusForbidden, "not_your_turn", "turn belongs to another player")
)

func errBadRequest(msg string) *APIError {
	return newError(http.StatusBadRequest, "invalid_argument", msg)
}

// sqlErrors — таблица «Коды ошибок» из docs/sql-functions.md.
var sqlErrors = map[string]struct {
	status int
	code   string
}{
	"23505": {http.StatusConflict, "login_already_taken"},
	"P0100": {http.StatusNotFound, "user_not_found"},
	"P0101": {http.StatusNotFound, "lobby_not_found"},
	"P0102": {http.StatusConflict, "lobby_not_open"},
	"P0103": {http.StatusConflict, "lobby_full"},
	"P0104": {http.StatusConflict, "already_joined"},
	"P0105": {http.StatusConflict, "not_enough_players"},
	"P0106": {http.StatusNotFound, "not_in_lobby"},
	"P0107": {http.StatusConflict, "already_in_session"},
	"P0200": {http.StatusNotFound, "turn_not_found"},
	"P0201": {http.StatusForbidden, "not_your_turn"},
	"P0202": {http.StatusConflict, "turn_already_closed"},
	"P0203": {http.StatusConflict, "no_throws_left"},
	"P0204": {http.StatusUnprocessableEntity, "combo_not_available"},
	"P0205": {http.StatusConflict, "throws_not_exhausted"},
	"P0206": {http.StatusUnprocessableEntity, "all_dice_fixed"},
	"P0207": {http.StatusConflict, "turn_already_open"},
	"P0301": {http.StatusConflict, "session_not_complete"},
	"P0302": {http.StatusConflict, "session_finished"},
	"22023": {http.StatusBadRequest, "invalid_argument"},
}

// toAPIError переводит ошибку (в том числе RAISE EXCEPTION из БД) в HTTP-ответ.
func toAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if m, ok := sqlErrors[pgErr.Code]; ok {
			msg := pgErr.Message
			// RAISE EXCEPTION 'invalid_argument: подробности' — оставляем только подробности.
			if rest, found := strings.CutPrefix(msg, m.code+": "); found {
				msg = rest
			}
			return newError(m.status, m.code, msg)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return newError(http.StatusNotFound, "not_found", "resource not found")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if apiErr := toAPIError(err); apiErr != nil {
		writeJSON(w, apiErr.Status, apiErr)
		return
	}
	slog.ErrorContext(r.Context(), "internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, newError(http.StatusInternalServerError, "internal", "internal server error"))
}
