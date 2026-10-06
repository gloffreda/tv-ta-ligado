package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/session"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

const token = "0123456789abcdef0123456789abcdef"

func sessionServer(t *testing.T) (*Server, *httptest.Server) {
	st := testfix.DB(t, "api")
	s := &Server{Store: st, OnDemand: true, AdminToken: token, SiteOrigin: "https://tv.exemplo.invalid", ConfigDir: testfix.Path("config"),
		Limits: session.Limits{MaxDur: time.Hour, MaxUSD: 2, IdleTimeout: 5 * time.Minute}, Tick: 20 * time.Millisecond}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv
}

func post(t *testing.T, url, origin, ip string, body any) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("X-Real-IP", ip)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestSessionStartRequiresTokenConfirmAndOrigin(t *testing.T) {
	s, srv := sessionServer(t)
	ctx := context.Background()
	u := srv.URL + "/v1/session/start"
	good := "https://tv.exemplo.invalid"
	cases := []struct {
		name, origin, ip string
		body             any
	}{
		{"sem token", good, "10.0.0.1", map[string]string{"confirm": "LIGAR"}},
		{"token errado", good, "10.0.0.1", map[string]string{"token": strings.Repeat("x", 32), "confirm": "LIGAR"}},
		{"sem confirmação", good, "10.0.0.1", map[string]string{"token": token}},
		{"confirmação errada", good, "10.0.0.2", map[string]string{"token": token, "confirm": "ligar"}},
		{"sem Origin", "", "10.0.0.2", map[string]string{"token": token, "confirm": "LIGAR"}},
		{"Origin de outro site", "https://malicioso.invalid", "10.0.0.2", map[string]string{"token": token, "confirm": "LIGAR"}},
		{"campo extra", good, "10.0.0.3", map[string]any{"token": token, "confirm": "LIGAR", "max_min": 999}},
	}
	for _, c := range cases {
		if code, _ := post(t, u, c.origin, c.ip, c.body); code != http.StatusForbidden {
			t.Errorf("%s: %d, quer 403", c.name, code)
		}
	}
	if _, ok, _ := s.Store.ActiveSession(ctx); ok {
		t.Fatal("nenhum pedido inválido pode ligar sessão")
	}
	var denied int
	s.Store.DB.QueryRow(ctx, `SELECT count(*) FROM system_events WHERE kind='session_denied'`).Scan(&denied)
	if denied != len(cases) {
		t.Fatalf("todo 403 é registrado: %d de %d", denied, len(cases))
	}
	// 4.º pedido do mesmo IP no mesmo minuto: 429, mesmo com tudo certo.
	if code, _ := post(t, u, good, "10.0.0.1", map[string]string{"token": token, "confirm": "LIGAR"}); code != http.StatusTooManyRequests {
		t.Fatalf("limite de 3 por minuto por IP: %d", code)
	}
	code, out := post(t, u, good, "10.0.0.9", map[string]string{"token": token, "confirm": "LIGAR"})
	if code != http.StatusCreated || out["stop_key"] == "" {
		t.Fatalf("pedido válido: %d %v", code, out)
	}
	ses, ok, _ := s.Store.ActiveSession(ctx)
	if !ok || ses.OriginIP != "10.0.0.9" || ses.MaxUSD != 2 || ses.Deadline.Sub(ses.StartedAt) != time.Hour {
		t.Fatalf("sessão: %+v", ses)
	}
	// Desligar: chave errada não; a chave da sessão sim.
	if code, _ := post(t, srv.URL+"/v1/session/stop", good, "10.0.0.5", map[string]string{"stop_key": "nao"}); code != http.StatusForbidden {
		t.Fatalf("stop com chave errada: %d", code)
	}
	if code, out := post(t, srv.URL+"/v1/session/stop", good, "10.0.0.5", map[string]any{"stop_key": out["stop_key"]}); code != 200 || out["stopped"] != true {
		t.Fatalf("stop: %d %v", code, out)
	}
	last, _, _ := s.Store.LastSession(ctx)
	if last.Status != "ended" || last.EndReason == nil || *last.EndReason != "button" {
		t.Fatalf("última sessão: %+v", last)
	}
	resp, _ := http.Get(srv.URL + "/v1/session")
	var st sessionState
	json.NewDecoder(resp.Body).Decode(&st)
	if st.Active || st.Last == nil || st.Last.ID != ses.ID || st.MaxMin != 60 {
		t.Fatalf("/v1/session: %+v", st)
	}
}

func TestSessionOriginFromForwardedHost(t *testing.T) {
	s, srv := sessionServer(t)
	s.SiteOrigin = "" // túnel rápido: o domínio muda; vale o host recebido pelo nginx
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/start", strings.NewReader(`{"token":"`+token+`","confirm":"LIGAR"}`))
	req.Header.Set("Origin", "https://abc.trycloudflare.com")
	req.Header.Set("X-Forwarded-Host", "abc.trycloudflare.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("%v %v", err, resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v1/session/stop", strings.NewReader(`{"token":"`+token+`"}`))
	req.Header.Set("Origin", "https://abc.trycloudflare.com")
	req.Header.Set("X-Forwarded-Host", "outro.trycloudflare.com")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("host diferente do Origin: %d", resp.StatusCode)
	}
}

func TestSSELimitPerIPAndViewers(t *testing.T) {
	s, srv := sessionServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	open := func(ip string) *http.Response {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
		req.Header.Set("X-Real-IP", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	var conns []*http.Response
	for i := 0; i < 3; i++ {
		r := open("10.1.1.1")
		if r.StatusCode != 200 {
			t.Fatalf("conexão %d: %d", i+1, r.StatusCode)
		}
		conns = append(conns, r)
	}
	if r := open("10.1.1.1"); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("4.ª conexão do mesmo IP: %d, quer 429", r.StatusCode)
	}
	other := open("10.1.1.2")
	if other.StatusCode != 200 {
		t.Fatal("outro IP entra")
	}
	if n := s.hub.Viewers(); n != 4 {
		t.Fatalf("espectadores: %d", n)
	}
	waitViewers := func(want int) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			v, _, _, _ := s.Store.Audience(context.Background())
			if v == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		v, _, _, _ := s.Store.Audience(context.Background())
		t.Fatalf("audience.viewers=%d, quer %d", v, want)
	}
	waitViewers(4)
	// o evento session chega a quem está conectado
	buf := make([]byte, 4096)
	n, _ := other.Body.Read(buf)
	for i := 0; i < 5 && !strings.Contains(string(buf[:n]), "event: session"); i++ {
		m, _ := other.Body.Read(buf[n:])
		n += m
	}
	if !strings.Contains(string(buf[:n]), "event: session") {
		t.Fatalf("SSE sem evento session: %s", buf[:n])
	}
	for _, c := range conns {
		c.Body.Close()
	}
	other.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for s.hub.Viewers() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	waitViewers(0)
	_, _, last, _ := s.Store.Audience(context.Background())
	if last == nil {
		t.Fatal("last_seen_at registrado enquanto havia espectador")
	}
}

func TestScheduleEndpoint(t *testing.T) {
	s, srv := sessionServer(t)
	s.Now = func() time.Time { return time.Date(2026, 10, 6, 9, 55, 0, 0, time.UTC) } // 6h55 em Brasília
	s.Loc, _ = time.LoadLocation("America/Sao_Paulo")
	s.PublicMode = "preview"
	resp, err := http.Get(srv.URL + "/v1/schedule")
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp.StatusCode)
	}
	var out struct {
		PublicMode string `json:"public_mode"`
		Badge      string `json:"badge"`
		Now        struct {
			Name  string   `json:"name"`
			Scene string   `json:"scene"`
			Cast  []string `json:"cast"`
		} `json:"now"`
		Programs []map[string]any `json:"programs"`
		Weather  []map[string]any `json:"weather"`
		Blocks   []map[string]any `json:"blocks"`
		Blackout []map[string]any `json:"blackout"`
		Personas map[string]struct {
			Name string                    `json:"name"`
			Rigs map[string]map[string]any `json:"rigs"`
		} `json:"personas"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.PublicMode != "preview" || out.Badge != "EM TESTE" || out.Now.Name != "Tempo com Glória Garoa" || out.Now.Scene != "tempo" {
		t.Fatalf("schedule: %+v", out)
	}
	if g := out.Personas["gloria"]; g.Name != "Glória Garoa" || g.Rigs["pixel"] == nil || g.Rigs["vector"] == nil {
		t.Fatalf("personas com rigs: %+v", out.Personas)
	}
	if len(out.Programs) < 10 || len(out.Weather) != 8 || len(out.Blocks) < 6 || len(out.Blackout) == 0 {
		t.Fatalf("grade de 2 dias: %d programas, %d tempos, %d blocos", len(out.Programs), len(out.Weather), len(out.Blocks))
	}
}

func TestBadgeRule(t *testing.T) {
	for _, c := range []struct {
		mode string
		bo   bool
		want string
	}{{"preview", false, "EM TESTE"}, {"live", false, "AO VIVO"}, {"live", true, "REPRISE"}, {"preview", true, "REPRISE"}} {
		if got := Badge(c.mode, c.bo); got != c.want {
			t.Errorf("%s/%v: %s", c.mode, c.bo, got)
		}
	}
}
