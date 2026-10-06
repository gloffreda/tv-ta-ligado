// Package api serve a linha do tempo: o que está no ar agora, a grade, eventos
// em SSE e os arquivos de áudio. Todos os clientes veem a mesma linha do tempo,
// ancorada no relógio do servidor (UTC).
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/session"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

type Server struct {
	Store    *store.Store
	Now      func() time.Time
	MediaDir string
	Tick     time.Duration // resolução do SSE (padrão 200 ms)
	// Sprint 3
	ConfigDir   string
	Loc         *time.Location
	PublicMode  string // preview | live
	OnDemand    bool   // RUN_MODE=on_demand
	SiteOrigin  string
	AdminToken  string
	Limits      session.Limits
	MaxSSEPerIP int // 3

	hub        *hub
	startLimit *limiter
	keysMu     sync.Mutex
	stopKeys   map[int64]string
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Line é a fala como o cliente precisa: texto checado, áudio, horário e visemas.
type Line struct {
	store.TimelineLine
	AudioURL string    `json:"audio_url"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

type Item struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	SegmentID *int64    `json:"segment_id,omitempty"`
	Block     *string   `json:"block,omitempty"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Status    string    `json:"status"`
	Lines     []Line    `json:"lines"`
}

func view(it store.TimelineItem) Item {
	out := Item{ID: it.ID, Kind: it.Kind, SegmentID: it.SegmentID, Block: it.Block, StartsAt: it.StartsAt.UTC(), EndsAt: it.EndsAt.UTC(), Status: it.Status}
	for _, l := range it.Lines {
		st := it.StartsAt.UTC().Add(time.Duration(l.OffsetMS) * time.Millisecond)
		out.Lines = append(out.Lines, Line{TimelineLine: l, AudioURL: "/media/" + l.AudioHash + ".ogg", StartsAt: st, EndsAt: st.Add(time.Duration(l.DurationMS) * time.Millisecond)})
	}
	return out
}

// Now é a resposta de GET /v1/now.
type Now struct {
	ServerTime   time.Time `json:"server_time"`
	Item         *Item     `json:"item"`
	Line         *Line     `json:"line"`
	PositionMS   int       `json:"position_ms"`               // posição dentro da fala atual
	ItemPosMS    int       `json:"item_position_ms"`          // posição dentro do item
	NextLine     *Line     `json:"next_line,omitempty"`       // se estiver numa pausa
	NextLineInMS int       `json:"next_line_in_ms,omitempty"` // quanto falta para ela
}

// At calcula o que está no ar no instante t.
func (s *Server) At(ctx context.Context, t time.Time) (Now, error) {
	t = t.UTC()
	out := Now{ServerTime: t}
	items, err := s.Store.TimelineRange(ctx, t, t.Add(time.Millisecond))
	if err != nil || len(items) == 0 {
		return out, err
	}
	it := view(items[0])
	out.Item = &it
	pos := int(t.Sub(it.StartsAt).Milliseconds())
	out.ItemPosMS = pos
	for i := range it.Lines {
		l := it.Lines[i]
		if pos >= l.OffsetMS && pos < l.OffsetMS+l.DurationMS {
			out.Line, out.PositionMS = &l, pos-l.OffsetMS
			return out, nil
		}
		if l.OffsetMS > pos {
			out.NextLine, out.NextLineInMS = &l, l.OffsetMS-pos
			return out, nil
		}
	}
	return out, nil
}

var mediaRe = regexp.MustCompile(`^[0-9a-f]{64}\.ogg$`)

func (s *Server) Handler() http.Handler {
	s.hub = newHub(s)
	s.startLimit = newLimiter(3, time.Minute)
	s.stopKeys = map[int64]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.DB.Ping(r.Context()); err != nil {
			writeJSON(w, 503, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "server_time": s.now()})
	})
	mux.HandleFunc("GET /v1/now", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.At(r.Context(), s.now())
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, n)
	})
	mux.HandleFunc("GET /v1/timeline", func(w http.ResponseWriter, r *http.Request) {
		now := s.now()
		from, err1 := parseTime(r.URL.Query().Get("from"), now.Add(-time.Minute))
		to, err2 := parseTime(r.URL.Query().Get("to"), now.Add(15*time.Minute))
		if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > 6*time.Hour {
			writeJSON(w, 400, map[string]string{"error": "from/to inválidos (RFC 3339 ou unix ms; janela até 6 h)"})
			return
		}
		items, err := s.Store.TimelineRange(r.Context(), from, to)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		out := make([]Item, len(items))
		for i, it := range items {
			out[i] = view(it)
		}
		writeJSON(w, 200, map[string]any{"server_time": now, "from": from, "to": to, "items": out})
	})
	mux.HandleFunc("GET /v1/events", s.events)
	mux.HandleFunc("GET /v1/schedule", s.schedule)
	mux.HandleFunc("GET /v1/session", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.sessionState(r.Context())
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, st)
	})
	mux.HandleFunc("POST /v1/session/start", s.sessionStart)
	mux.HandleFunc("POST /v1/session/stop", s.sessionStop)
	mux.HandleFunc("GET /media/{file}", func(w http.ResponseWriter, r *http.Request) {
		f := r.PathValue("file")
		if !mediaRe.MatchString(f) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("Content-Type", "audio/ogg")
		http.ServeFile(w, r, filepath.Join(s.MediaDir, f))
	})
	return mux
}

func parseTime(v string, def time.Time) (time.Time, error) {
	if v == "" {
		return def, nil
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	return t.UTC(), err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// events: SSE com item_started, line_started, item_scheduled e session.
// No máximo MaxSSEPerIP conexões por IP (429 além disso).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming não suportado", 500)
		return
	}
	sb, ok := s.hub.join(clientIP(r))
	if !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "conexões demais deste endereço"})
		return
	}
	defer s.hub.leave(sb)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		fl.Flush()
	}
	fmt.Fprint(w, "retry: 2000\n\n")
	fl.Flush()
	ctx := r.Context()
	var lastItem int64 = -1
	lastLine := -1
	var maxID int64 = -1
	var lastSession []byte
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		var snap snapshot
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
			continue
		case snap = <-sb.ch:
		}
		now := snap.at
		if b, _ := json.Marshal(snap.session); string(b) != string(lastSession) {
			lastSession = b
			send("session", snap.session)
		}
		if maxID >= 0 && snap.maxID > maxID {
			for _, it := range snap.window {
				if it.ID > maxID {
					send("item_scheduled", view(it))
				}
			}
		}
		maxID = snap.maxID
		for _, raw := range snap.window {
			if now.Before(raw.StartsAt) || !now.Before(raw.EndsAt) {
				continue
			}
			it := view(raw)
			if it.ID != lastItem {
				lastItem, lastLine = it.ID, -1
				send("item_started", map[string]any{"server_time": now, "item": it, "item_position_ms": now.Sub(it.StartsAt).Milliseconds()})
			}
			pos := int(now.Sub(it.StartsAt).Milliseconds())
			for _, l := range it.Lines {
				if pos >= l.OffsetMS && pos < l.OffsetMS+l.DurationMS && l.Seq != lastLine {
					lastLine = l.Seq
					send("line_started", map[string]any{"server_time": now, "item_id": it.ID, "line": l, "position_ms": pos - l.OffsetMS})
				}
			}
		}
	}
}
