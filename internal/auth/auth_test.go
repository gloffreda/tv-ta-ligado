package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correto-cavalo-bateria")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(h, "$ ") || !strings.HasPrefix(h, "argon2id.v19.m65536.t3.p2.") {
		t.Fatalf("formato: %s", h)
	}
	if !VerifyPassword(h, "correto-cavalo-bateria") || VerifyPassword(h, "correto-cavalo-bateriA") || VerifyPassword("lixo", "x") {
		t.Fatal("verificação")
	}
	if _, err := HashPassword("curta"); err == nil {
		t.Fatal("senha curta deve falhar")
	}
}

func TestSignedCookie(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := Signer{Secret: "segredo", PasswordHash: "h1"}
	tok, exp, err := s.Issue(now)
	if err != nil || exp.Sub(now) != SessionTTL {
		t.Fatal(err)
	}
	if !s.Valid(tok, now.Add(29*24*time.Hour)) {
		t.Fatal("válido por 30 dias")
	}
	if s.Valid(tok, now.Add(31*24*time.Hour)) {
		t.Fatal("expirado")
	}
	if s.Valid(tok[:len(tok)-2]+"xx", now) || s.Valid("v1.9999999999.abc.def", now) {
		t.Fatal("assinatura adulterada")
	}
	if (Signer{Secret: "segredo", PasswordHash: "h2"}).Valid(tok, now) {
		t.Fatal("trocar a senha desloga")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter()
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("1.1.1.1", now); !ok {
			t.Fatal("5 por minuto")
		}
		l.Result("1.1.1.1", false, now)
	}
	if ok, why := l.Allow("1.1.1.1", now); ok || why != "rate" {
		t.Fatal("6.ª no mesmo minuto")
	}
	if ok, _ := l.Allow("2.2.2.2", now); !ok {
		t.Fatal("outro IP")
	}
	now = now.Add(61 * time.Second)
	for i := 0; i < 5; i++ {
		l.Allow("1.1.1.1", now)
		blocked := l.Result("1.1.1.1", false, now)
		if blocked != (i == 4) {
			t.Fatalf("bloqueio na 10.ª falha (i=%d)", i)
		}
	}
	if ok, why := l.Allow("1.1.1.1", now.Add(14*time.Minute)); ok || why != "blocked" {
		t.Fatal("bloqueado por 15 min")
	}
	if ok, _ := l.Allow("1.1.1.1", now.Add(16*time.Minute)); !ok {
		t.Fatal("libera depois de 15 min")
	}
}
