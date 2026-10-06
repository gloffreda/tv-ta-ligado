package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// sessionState: o que a página precisa para mostrar FORA DO AR ou o canal.
type sessionState struct {
	Active  bool           `json:"active"`
	OnAir   bool           `json:"on_air"` // RUN_MODE=always: sempre no ar
	Last    *store.Session `json:"last,omitempty"`
	MaxMin  int            `json:"max_min"`
	MaxUSD  float64        `json:"max_usd"`
	Viewers int            `json:"viewers"`
}

func (s *Server) sessionState(ctx context.Context) (sessionState, error) {
	st := sessionState{MaxMin: int(s.Limits.MaxDur / time.Minute), MaxUSD: s.Limits.MaxUSD, OnAir: !s.OnDemand}
	last, ok, err := s.Store.LastSession(ctx)
	if err != nil {
		return st, err
	}
	if ok {
		if last.Status == "active" {
			if sp, err := s.Store.SessionSpent(ctx, last, s.now()); err == nil {
				last.SpentUSD = sp
			}
		}
		st.Last = &last
		st.Active = last.Status == "active"
	}
	st.OnAir = st.OnAir || st.Active
	if s.hub != nil {
		st.Viewers = s.hub.Viewers()
	}
	return st, nil
}

// clientIP: o nginx do site põe o IP real (CF-Connecting-IP) em X-Real-IP; a
// api não tem porta publicada, então só o nginx chega aqui.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// limiter: no máximo n eventos por janela, por chave (IP).
type limiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var keep []time.Time
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.n {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

func eq(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// originOK: Origin igual ao domínio do site (SITE_ORIGIN, ou o host que o
// nginx recebeu, sempre em https).
func (s *Server) originOK(r *http.Request) bool {
	o := strings.TrimRight(r.Header.Get("Origin"), "/")
	if o == "" {
		return false
	}
	if s.SiteOrigin != "" {
		return o == s.SiteOrigin
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return host != "" && o == "https://"+host
}

type startReq struct {
	Token   string `json:"token"`
	Confirm string `json:"confirm"`
}

type stopReq struct {
	StopKey string `json:"stop_key"`
	Token   string `json:"token"`
}

func (s *Server) deny(w http.ResponseWriter, r *http.Request, action, reason string) {
	ip := clientIP(r)
	slog.Warn("sessão: pedido recusado", "acao", action, "motivo", reason, "ip", ip, "origin", r.Header.Get("Origin"))
	_ = s.Store.Event(context.WithoutCancel(r.Context()), "session_denied", map[string]any{
		"action": action, "reason": reason, "ip": ip, "origin": r.Header.Get("Origin"), "user_agent": r.UserAgent()})
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
}

// readJSON lê um corpo pequeno (até 4 KiB).
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) sessionStart(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.startLimit.allow(ip, s.now()) {
		slog.Warn("sessão: limite de tentativas", "ip", ip)
		_ = s.Store.Event(r.Context(), "session_denied", map[string]any{"action": "start", "reason": "rate_limit", "ip": ip})
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas tentativas; espere um minuto"})
		return
	}
	if !s.OnDemand {
		s.deny(w, r, "start", "run_mode_always")
		return
	}
	if !s.originOK(r) {
		s.deny(w, r, "start", "origin")
		return
	}
	var req startReq
	if err := readJSON(r, &req); err != nil {
		s.deny(w, r, "start", "body")
		return
	}
	if req.Confirm != "LIGAR" {
		s.deny(w, r, "start", "confirm")
		return
	}
	if s.AdminToken == "" || !eq(req.Token, s.AdminToken) {
		s.deny(w, r, "start", "token")
		return
	}
	ses, created, err := s.Store.StartSession(r.Context(), s.now(), s.Limits.MaxDur, s.Limits.MaxUSD, ip, r.UserAgent())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	key := s.stopKeyFor(ses.ID, created)
	slog.Info("sessão ligada pela página", "sessao", ses.ID, "nova", created, "ip", ip)
	_ = s.Store.Event(r.Context(), "session_started", map[string]any{"session_id": ses.ID, "created": created, "ip": ip})
	code := http.StatusCreated
	if !created {
		code = http.StatusOK
	}
	writeJSON(w, code, map[string]any{"session": ses, "stop_key": key, "created": created})
}

// stopKeyFor: chave de desligar da sessão (só em memória; devolvida a quem ligou).
func (s *Server) stopKeyFor(id int64, created bool) string {
	s.keysMu.Lock()
	defer s.keysMu.Unlock()
	if k, ok := s.stopKeys[id]; ok && !created {
		return k
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	k := hex.EncodeToString(b)
	s.stopKeys[id] = k
	return k
}

func (s *Server) sessionStop(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		s.deny(w, r, "stop", "origin")
		return
	}
	var req stopReq
	if err := readJSON(r, &req); err != nil {
		s.deny(w, r, "stop", "body")
		return
	}
	ses, ok, err := s.Store.ActiveSession(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, 200, map[string]any{"stopped": false, "reason": "sem sessão ativa"})
		return
	}
	s.keysMu.Lock()
	key := s.stopKeys[ses.ID]
	s.keysMu.Unlock()
	if !eq(req.StopKey, key) && !(s.AdminToken != "" && eq(req.Token, s.AdminToken)) {
		s.deny(w, r, "stop", "key")
		return
	}
	ended, err := s.Store.EndSession(r.Context(), ses.ID, "button", s.now())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	slog.Info("sessão desligada pela página", "sessao", ses.ID, "ip", clientIP(r))
	_ = s.Store.Event(r.Context(), "session_ended", map[string]any{"session_id": ses.ID, "reason": "button"})
	writeJSON(w, 200, map[string]any{"stopped": ended})
}
