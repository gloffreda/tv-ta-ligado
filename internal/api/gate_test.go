package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/auth"
	"github.com/gloffreda/tv-ta-ligado/internal/session"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

const sitePassword = "senha-de-teste-bem-longa"

func gateServer(t *testing.T, gate bool) (*Server, *httptest.Server) {
	st := testfix.DB(t, "api")
	h, err := auth.HashPassword(sitePassword)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st, OnDemand: true, SiteOrigin: "https://tv.exemplo.invalid", ConfigDir: testfix.Path("config"), MediaDir: t.TempDir(),
		Limits: session.Limits{MaxDur: time.Hour, MaxUSD: 2, IdleTimeout: 5 * time.Minute}, Tick: 20 * time.Millisecond,
		Gate: gate, Auth: auth.Signer{Secret: "segredo-de-teste", PasswordHash: h}}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv
}

func do(t *testing.T, method, url, origin, ip, body string, cookie *http.Cookie) *http.Response {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("X-Real-IP", ip)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

const site = "https://tv.exemplo.invalid"

func login(t *testing.T, srv *httptest.Server, ip string) *http.Cookie {
	r := do(t, "POST", srv.URL+"/v1/auth/login", site, ip, `{"password":"`+sitePassword+`"}`, nil)
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("login: %d", r.StatusCode)
	}
	for _, c := range r.Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	t.Fatal("sem cookie")
	return nil
}

func TestGateBlocksAPIAndMediaWithoutLogin(t *testing.T) {
	_, srv := gateServer(t, true)
	for _, p := range []string{"/v1/now", "/v1/timeline", "/v1/schedule", "/v1/session", "/v1/events", "/media/" + strings.Repeat("a", 64) + ".ogg"} {
		if r := do(t, "GET", srv.URL+p, "", "10.9.0.1", "", nil); r.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s sem login: %d, quer 401", p, r.StatusCode)
		}
	}
	// livres sem login
	for _, p := range []string{"/healthz", "/v1/auth/me"} {
		if r := do(t, "GET", srv.URL+p, "", "10.9.0.1", "", nil); r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	// o nginx consulta /v1/auth/check: 401 sem login → 302 para /entrar
	if r := do(t, "GET", srv.URL+"/v1/auth/check", "", "10.9.0.1", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("check sem login: %d", r.StatusCode)
	}
	c := login(t, srv, "10.9.0.2")
	if r := do(t, "GET", srv.URL+"/v1/auth/check", "", "10.9.0.2", "", c); r.StatusCode != http.StatusNoContent {
		t.Fatalf("check com login: %d", r.StatusCode)
	}
	if r := do(t, "GET", srv.URL+"/v1/now", "", "10.9.0.2", "", c); r.StatusCode != 200 {
		t.Fatalf("/v1/now com login: %d", r.StatusCode)
	}
	// cookie adulterado
	bad := *c
	bad.Value = c.Value[:len(c.Value)-3] + "AAA"
	if r := do(t, "GET", srv.URL+"/v1/now", "", "10.9.0.2", "", &bad); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie adulterado: %d", r.StatusCode)
	}
	// sair apaga o cookie
	r := do(t, "POST", srv.URL+"/v1/auth/logout", site, "10.9.0.2", "", c)
	if r.StatusCode != http.StatusNoContent || len(r.Cookies()) == 0 || r.Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout: %d %v", r.StatusCode, r.Cookies())
	}
}

func TestLoginCookieFlags(t *testing.T) {
	_, srv := gateServer(t, true)
	c := login(t, srv, "10.9.1.1")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("flags: HttpOnly=%v Secure=%v SameSite=%v Path=%q", c.HttpOnly, c.Secure, c.SameSite, c.Path)
	}
	if c.MaxAge != 30*24*3600 {
		t.Fatalf("validade: %d s, quer 30 dias", c.MaxAge)
	}
	if r := do(t, "POST", srv.URL+"/v1/auth/login", site, "10.9.1.2", `{"password":"errada-errada-errada"}`, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("senha errada: %d", r.StatusCode)
	}
	if r := do(t, "POST", srv.URL+"/v1/auth/login", "https://outro.invalid", "10.9.1.3", `{"password":"`+sitePassword+`"}`, nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("login de outro Origin: %d", r.StatusCode)
	}
}

func TestLoginRateLimitAndBlock(t *testing.T) {
	s, srv := gateServer(t, true)
	ip := "10.9.2.1"
	wrong := `{"password":"errada-errada-errada"}`
	for i := 0; i < 5; i++ {
		if r := do(t, "POST", srv.URL+"/v1/auth/login", site, ip, wrong, nil); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("tentativa %d: %d", i+1, r.StatusCode)
		}
	}
	if r := do(t, "POST", srv.URL+"/v1/auth/login", site, ip, `{"password":"`+sitePassword+`"}`, nil); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6.ª tentativa no minuto (mesmo com a senha certa): %d", r.StatusCode)
	}
	// mais 5 falhas no minuto seguinte → 10 falhas → bloqueio de 15 min
	base := time.Now()
	s.Now = func() time.Time { return base.Add(61 * time.Second) }
	for i := 0; i < 5; i++ {
		do(t, "POST", srv.URL+"/v1/auth/login", site, ip, wrong, nil)
	}
	s.Now = func() time.Time { return base.Add(5 * time.Minute) }
	if r := do(t, "POST", srv.URL+"/v1/auth/login", site, ip, `{"password":"`+sitePassword+`"}`, nil); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("bloqueado por 15 min: %d", r.StatusCode)
	}
	s.Now = func() time.Time { return base.Add(17 * time.Minute) }
	if r := do(t, "POST", srv.URL+"/v1/auth/login", site, ip, `{"password":"`+sitePassword+`"}`, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("depois de 15 min: %d", r.StatusCode)
	}
	var fails int
	s.Store.DB.QueryRow(t.Context(), `SELECT count(*) FROM system_events WHERE kind='login_failed'`).Scan(&fails)
	if fails != 10 {
		t.Fatalf("falhas registradas: %d", fails)
	}
	var leaked int
	s.Store.DB.QueryRow(t.Context(), `SELECT count(*) FROM system_events WHERE detail::text LIKE '%errada%'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("a senha não pode ir para o registro")
	}
}

func TestStartSessionWithCookie(t *testing.T) {
	s, srv := gateServer(t, true)
	u := srv.URL + "/v1/session/start"
	if r := do(t, "POST", u, site, "10.9.3.1", `{}`, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sem cookie: %d, quer 401", r.StatusCode)
	}
	c := login(t, srv, "10.9.3.2")
	if r := do(t, "POST", u, "https://malicioso.invalid", "10.9.3.2", `{}`, c); r.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie + Origin errado: %d, quer 403", r.StatusCode)
	}
	if r := do(t, "POST", u, "", "10.9.3.3", `{}`, c); r.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie sem Origin: %d, quer 403", r.StatusCode)
	}
	if _, ok, _ := s.Store.ActiveSession(t.Context()); ok {
		t.Fatal("nada ligou ainda")
	}
	if r := do(t, "POST", u, site, "10.9.3.4", `{}`, c); r.StatusCode != http.StatusCreated {
		t.Fatalf("cookie + Origin certo: %d, quer 201", r.StatusCode)
	}
	// desligar com o cookie, sem chave nem token
	if r := do(t, "POST", srv.URL+"/v1/session/stop", site, "10.9.3.4", `{}`, c); r.StatusCode != 200 {
		t.Fatalf("stop com cookie: %d", r.StatusCode)
	}
	if _, ok, _ := s.Store.ActiveSession(t.Context()); ok {
		t.Fatal("desligou")
	}
}

func TestGateOffOpenToViewersButStartNeedsLogin(t *testing.T) {
	_, srv := gateServer(t, false)
	if r := do(t, "GET", srv.URL+"/v1/now", "", "10.9.4.1", "", nil); r.StatusCode != 200 {
		t.Fatalf("SITE_GATE=off: /v1/now aberto, veio %d", r.StatusCode)
	}
	if r := do(t, "GET", srv.URL+"/v1/auth/check", "", "10.9.4.1", "", nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("páginas abertas: %d", r.StatusCode)
	}
	resp, _ := http.Get(srv.URL + "/v1/auth/me")
	var me map[string]bool
	json.NewDecoder(resp.Body).Decode(&me)
	if me["gate"] || me["logged"] {
		t.Fatalf("me: %v", me)
	}
	if r := do(t, "POST", srv.URL+"/v1/session/start", site, "10.9.4.1", `{}`, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ligar sem login, mesmo com o portão aberto: %d", r.StatusCode)
	}
}
