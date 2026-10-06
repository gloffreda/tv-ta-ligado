package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/auth"
)

// logged: o pedido traz um cookie de login válido?
func (s *Server) logged(r *http.Request) bool {
	c, err := r.Cookie(auth.CookieName)
	return err == nil && s.Auth.Valid(c.Value, s.now())
}

// public: caminhos que não exigem login mesmo com o portão ligado.
func public(path string) bool {
	return path == "/healthz" || path == "/v1/auth/login" || path == "/v1/auth/logout" || path == "/v1/auth/check" || path == "/v1/auth/me"
}

// gate: com SITE_GATE=on, /v1/* e /media/* exigem login (401). As páginas são
// barradas no nginx (auth_request em /v1/auth/check → 302 para /entrar).
func (s *Server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if s.Gate && !public(p) && (strings.HasPrefix(p, "/v1/") || strings.HasPrefix(p, "/media/")) && !s.logged(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login necessário"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type loginReq struct {
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	now := s.now()
	if !s.originOK(r) {
		slog.Warn("login recusado", "motivo", "origin", "ip", ip, "origin", r.Header.Get("Origin"))
		_ = s.Store.Event(r.Context(), "login_denied", map[string]any{"reason": "origin", "ip": ip})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if ok, why := s.loginLimit.Allow(ip, now); !ok {
		slog.Warn("login recusado", "motivo", why, "ip", ip)
		_ = s.Store.Event(r.Context(), "login_denied", map[string]any{"reason": why, "ip": ip})
		msg := "muitas tentativas; espere um minuto"
		if why == "blocked" {
			msg = "muitas senhas erradas; tente de novo em 15 minutos"
		}
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": msg})
		return
	}
	if s.Auth.PasswordHash == "" || s.Auth.Secret == "" {
		slog.Error("login: SITE_PASSWORD_HASH ou SITE_SESSION_SECRET ausente (make set-password)")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "senha do site não configurada"})
		return
	}
	var req loginReq
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pedido inválido"})
		return
	}
	ok := auth.VerifyPassword(s.Auth.PasswordHash, req.Password)
	blocked := s.loginLimit.Result(ip, ok, now)
	if !ok {
		slog.Warn("login: senha errada", "ip", ip, "bloqueado_15min", blocked) // a senha nunca vai para o log
		_ = s.Store.Event(context.WithoutCancel(r.Context()), "login_failed", map[string]any{"ip": ip, "blocked": blocked, "user_agent": r.UserAgent()})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "senha incorreta"})
		return
	}
	tok, exp, err := s.Auth.Issue(now)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: tok, Path: "/", Expires: exp, MaxAge: int(auth.SessionTTL / time.Second),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	slog.Info("login ok", "ip", ip)
	_ = s.Store.Event(r.Context(), "login_ok", map[string]any{"ip": ip, "user_agent": r.UserAgent()})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// check: usado pelo nginx (auth_request) para liberar as páginas.
func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	if !s.Gate || s.logged(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"gate": s.Gate, "logged": s.logged(r)})
}
