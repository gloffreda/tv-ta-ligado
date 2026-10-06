// Package auth: senha do site (só o hash argon2id fica no .env), cookie de
// sessão assinado e limite de tentativas de login.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// CookieName do login do site.
const CookieName = "tvtl_sess"

// SessionTTL: validade do login.
const SessionTTL = 30 * 24 * time.Hour

var b64 = base64.RawURLEncoding

// Parâmetros do argon2id (RFC 9106, segunda recomendação: 64 MiB, 3 passadas).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword: "argon2id.v19.m65536.t3.p2.<sal>.<hash>". Sem "$", para não
// ser interpolado pelo Compose ao ler o .env.
func HashPassword(pw string) (string, error) {
	if len(pw) < 12 {
		return "", errors.New("senha curta demais (mínimo 12 caracteres)")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id.v%d.m%d.t%d.p%d.%s.%s", argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(h)), nil
}

// VerifyPassword compara em tempo constante.
func VerifyPassword(encoded, pw string) bool {
	p := strings.Split(encoded, ".")
	if len(p) != 7 || p[0] != "argon2id" {
		return false
	}
	num := func(s, prefix string) (uint32, bool) {
		if !strings.HasPrefix(s, prefix) {
			return 0, false
		}
		v, err := strconv.ParseUint(s[len(prefix):], 10, 32)
		return uint32(v), err == nil
	}
	m, ok1 := num(p[2], "m")
	t, ok2 := num(p[3], "t")
	th, ok3 := num(p[4], "p")
	salt, err1 := b64.DecodeString(p[5])
	want, err2 := b64.DecodeString(p[6])
	if !ok1 || !ok2 || !ok3 || err1 != nil || err2 != nil || th == 0 || th > 255 || m > 1<<20 || t > 10 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, uint8(th), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Signer assina o cookie. A chave mistura o segredo com o hash da senha:
// trocar a senha desloga todo mundo.
type Signer struct {
	Secret       string
	PasswordHash string
}

func (s Signer) key() []byte {
	m := hmac.New(sha256.New, []byte(s.Secret))
	m.Write([]byte(s.PasswordHash))
	return m.Sum(nil)
}

func (s Signer) mac(payload string) string {
	m := hmac.New(sha256.New, s.key())
	m.Write([]byte(payload))
	return b64.EncodeToString(m.Sum(nil))
}

// Issue: "v1.<expira unix>.<nonce>.<mac>".
func (s Signer) Issue(now time.Time) (string, time.Time, error) {
	if s.Secret == "" || s.PasswordHash == "" {
		return "", time.Time{}, errors.New("SITE_SESSION_SECRET ou SITE_PASSWORD_HASH ausente")
	}
	n := make([]byte, 12)
	if _, err := rand.Read(n); err != nil {
		return "", time.Time{}, err
	}
	exp := now.Add(SessionTTL)
	payload := fmt.Sprintf("v1.%d.%s", exp.Unix(), b64.EncodeToString(n))
	return payload + "." + s.mac(payload), exp, nil
}

// Valid: assinatura certa e dentro da validade.
func (s Signer) Valid(token string, now time.Time) bool {
	if s.Secret == "" || s.PasswordHash == "" {
		return false
	}
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return false
	}
	payload, mac := token[:i], token[i+1:]
	if subtle.ConstantTimeCompare([]byte(mac), []byte(s.mac(payload))) != 1 {
		return false
	}
	p := strings.Split(payload, ".")
	if len(p) != 3 || p[0] != "v1" {
		return false
	}
	exp, err := strconv.ParseInt(p[1], 10, 64)
	return err == nil && now.Unix() < exp
}

// Limiter: no máximo PerMinute tentativas por minuto por IP; depois de
// MaxFails falhas (sem acerto no meio), bloqueia por Block.
type Limiter struct {
	PerMinute int           // 5
	MaxFails  int           // 10
	Block     time.Duration // 15 min

	mu    sync.Mutex
	hits  map[string][]time.Time
	fails map[string]int
	until map[string]time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{PerMinute: 5, MaxFails: 10, Block: 15 * time.Minute,
		hits: map[string][]time.Time{}, fails: map[string]int{}, until: map[string]time.Time{}}
}

// Allow: pode tentar agora? Devolve o motivo da recusa ("rate" ou "blocked").
func (l *Limiter) Allow(ip string, now time.Time) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if u, ok := l.until[ip]; ok {
		if now.Before(u) {
			return false, "blocked"
		}
		delete(l.until, ip)
		l.fails[ip] = 0
	}
	var keep []time.Time
	for _, t := range l.hits[ip] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.PerMinute {
		l.hits[ip] = keep
		return false, "rate"
	}
	l.hits[ip] = append(keep, now)
	return true, ""
}

// Result registra o resultado; devolve true se a falha bloqueou o IP agora.
func (l *Limiter) Result(ip string, ok bool, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ok {
		delete(l.fails, ip)
		return false
	}
	l.fails[ip]++
	if l.fails[ip] >= l.MaxFails {
		l.until[ip] = now.Add(l.Block)
		return true
	}
	return false
}
