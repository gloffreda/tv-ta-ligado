package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// hub: uma única rotina lê o banco (janela da linha do tempo e sessão) e
// distribui para todas as conexões SSE. Sem espectador, ela para.
type hub struct {
	s    *Server
	mu   sync.Mutex
	subs map[*sub]struct{}
	byIP map[string]int
	stop context.CancelFunc

	lastPub  time.Time
	lastSent int
	pub      chan audience // um só escritor: as gravações não chegam fora de ordem
}

type audience struct {
	n  int
	at time.Time
}

type snapshot struct {
	at      time.Time
	window  []store.TimelineItem
	maxID   int64
	session sessionState
}

type sub struct {
	ip string
	ch chan snapshot
}

func newHub(s *Server) *hub {
	h := &hub{s: s, subs: map[*sub]struct{}{}, byIP: map[string]int{}, lastSent: -1, pub: make(chan audience, 1)}
	go h.writer()
	return h
}

// writer grava a audiência mais recente (bloqueado no canal quando não há nada).
func (h *hub) writer() {
	for a := range h.pub {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.s.Store.SetViewers(ctx, a.n, a.at); err != nil {
			slog.Warn("audiência", "erro", err)
		}
		cancel()
	}
}

// join registra a conexão; false se o IP já tem o máximo de conexões.
func (h *hub) join(ip string) (*sub, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	max := h.s.MaxSSEPerIP
	if max <= 0 {
		max = 3
	}
	if h.byIP[ip] >= max {
		return nil, false
	}
	sb := &sub{ip: ip, ch: make(chan snapshot, 4)}
	h.subs[sb] = struct{}{}
	h.byIP[ip]++
	if h.stop == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.stop = cancel
		go h.loop(ctx)
	}
	h.publishLocked()
	return sb, true
}

func (h *hub) leave(sb *sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[sb]; !ok {
		return
	}
	delete(h.subs, sb)
	if h.byIP[sb.ip]--; h.byIP[sb.ip] <= 0 {
		delete(h.byIP, sb.ip)
	}
	h.publishLocked()
	if len(h.subs) == 0 && h.stop != nil {
		h.stop()
		h.stop = nil
	}
}

// Viewers: conexões SSE abertas agora.
func (h *hub) Viewers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// publishLocked grava a audiência quando muda (e a cada 15 s com gente vendo,
// para o supervisor saber que ainda há espectador).
func (h *hub) publishLocked() {
	n := len(h.subs)
	now := h.s.now()
	if n == h.lastSent && (n == 0 || now.Sub(h.lastPub) < 15*time.Second) {
		return
	}
	h.lastSent, h.lastPub = n, now
	for {
		select {
		case h.pub <- audience{n, now}:
			return
		default:
			select { // troca o valor pendente pelo mais novo
			case <-h.pub:
			default:
			}
		}
	}
}

func (h *hub) loop(ctx context.Context) {
	tick := h.s.Tick
	if tick == 0 {
		tick = 200 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	var snap snapshot
	for {
		now := h.s.now()
		if snap.window == nil || now.Sub(snap.at) > 2*time.Second {
			w, err := h.s.Store.TimelineRange(ctx, now.Add(-time.Minute), now.Add(30*time.Minute))
			if err != nil && ctx.Err() == nil {
				slog.Warn("sse", "erro", err)
			}
			if w == nil {
				w = []store.TimelineItem{}
			}
			id, _ := h.s.Store.MaxTimelineID(ctx)
			ss, _ := h.s.sessionState(ctx)
			snap = snapshot{at: now, window: w, maxID: id, session: ss}
		}
		cur := snap
		cur.at = now
		h.mu.Lock()
		for sb := range h.subs {
			select {
			case sb.ch <- cur:
			default: // cliente lento: pula este tique
			}
		}
		h.publishLocked()
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
