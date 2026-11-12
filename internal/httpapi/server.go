// Package httpapi — REST API по сценариям docs/api-schema.md.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/seventwogth/databases-itmo/api"
	"github.com/seventwogth/databases-itmo/internal/auth"
	"github.com/seventwogth/databases-itmo/internal/store"
)

type Server struct {
	store  *store.Store
	tokens *auth.Tokens
}

func New(st *store.Store, tokens *auth.Tokens) *Server {
	return &Server{store: st, tokens: tokens}
}

// Handler собирает маршруты. Пути и тела описаны в api/openapi.yaml.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Документация
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(api.OpenAPI)
	})
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.URL("/openapi.yaml")))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/index.html", http.StatusFound)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// 1. Авторизация / регистрация — без токена
	mux.HandleFunc("POST /api/auth/register", s.handle(s.register))
	mux.HandleFunc("POST /api/auth/login", s.handle(s.login))

	// Всё остальное — с Auth Token
	authed := func(pattern string, h handlerFunc) {
		mux.HandleFunc(pattern, s.handle(s.requireAuth(h)))
	}
	authed("GET /api/profile", s.profile)
	authed("GET /api/combinations", s.combinations)

	// 2–4. Лобби
	authed("GET /api/lobbies", s.listLobbies)
	authed("POST /api/lobbies", s.createLobby)
	authed("GET /api/lobbies/{session_id}", s.getLobby)
	authed("POST /api/lobbies/{session_id}/join", s.joinLobby)
	authed("POST /api/lobbies/{session_id}/leave", s.leaveLobby)

	// 5. Начало игры и первый бросок
	authed("POST /api/lobbies/{session_id}/start", s.startGame)

	// 6–7. Ход
	authed("POST /api/game/reroll", s.reroll)
	authed("GET /api/turns/{turn_id}", s.getTurn)
	authed("POST /api/turns/{turn_id}/finish", s.finishTurn)
	authed("POST /api/turns/{turn_id}/skip", s.skipTurn)

	// 8–9. Состояние партии и выход
	authed("GET /api/sessions/{session_id}/state", s.sessionState)
	authed("POST /api/sessions/{session_id}/leave", s.leaveMatch)

	return withRecover(withLogging(withCORS(mux)))
}

// ---------- Обёртки ----------

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) handle(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(w, r, err)
		}
	}
}

type ctxKey struct{}

func userID(ctx context.Context) int32 { return ctx.Value(ctxKey{}).(int32) }

// requireAuth проверяет Bearer-токен и отмечает активность (touch_last_active).
func (s *Server) requireAuth(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			return errUnauthorized
		}
		uid, err := s.tokens.Parse(strings.TrimSpace(raw))
		if err != nil {
			return errUnauthorized
		}
		if err := s.store.Q().TouchLastActive(r.Context(), uid); err != nil {
			// пользователь удалён — токен больше не действителен
			if e := toAPIError(err); e != nil && e.Code == "user_not_found" {
				return errUnauthorized
			}
			return err
		}
		return next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, uid)))
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/swagger/") {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "dur", time.Since(start).Round(time.Microsecond))
	})
}

func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				writeError(w, r, fmt.Errorf("panic: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------- Разбор запросов ----------

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errBadRequest("request body is required")
		}
		return errBadRequest("invalid JSON: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int32, error) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 32)
	if err != nil || v <= 0 {
		return 0, errBadRequest(name + " must be a positive integer")
	}
	return int32(v), nil
}

func queryInt(r *http.Request, name string, def, min, max int32) (int32, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || int32(v) < min || int32(v) > max {
		return 0, errBadRequest(fmt.Sprintf("%s must be an integer in %d..%d", name, min, max))
	}
	return int32(v), nil
}
