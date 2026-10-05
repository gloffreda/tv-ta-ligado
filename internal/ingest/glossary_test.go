package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceTextBCBPageAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/paginasite/sitebcb/controleinflacao/taxaselic":
			w.Write([]byte(`{"metatags":{"Titulo":"Taxa Selic"},"conteudo":"<h1>Taxa Selic</h1><p>A taxa Selic é a taxa básica de juros da economia.</p>"}`))
		default: // o BCB responde 200 para caminho inexistente, com metatags nulas
			w.Write([]byte(`{"metatags":null,"conteudo":null}`))
		}
	}))
	defer srv.Close()
	in := &Ingester{HTTP: srv.Client(), UA: "test"}
	text, err := in.sourceText(context.Background(), srv.URL+"/api/paginasite/sitebcb/controleinflacao/taxaselic")
	if err != nil || squash(text) != squash("Taxa Selic\n\nA taxa Selic é a taxa básica de juros da economia.") {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if _, err := in.sourceText(context.Background(), srv.URL+"/api/paginasite/sitebcb/nao/existe"); err == nil {
		t.Fatal("página inexistente do BCB deve invalidar o termo")
	}
}

func TestGetRetryBackoff(t *testing.T) {
	old := BCBBackoff
	BCBBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { BCBBackoff = old }()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`ok`))
	}))
	defer srv.Close()
	in := &Ingester{HTTP: srv.Client(), UA: "test"}
	body, err := in.getRetry(context.Background(), []string{srv.URL + "/a", srv.URL + "/a", srv.URL + "/b"})
	if err != nil || string(body) != "ok" || hits != 3 {
		t.Fatalf("body=%q err=%v hits=%d", body, err, hits)
	}
	atomic.StoreInt32(&hits, -10)
	if _, err := in.getRetry(context.Background(), []string{srv.URL}); err == nil || hits != -7 {
		t.Fatalf("3 tentativas e falha: err=%v hits=%d", err, hits)
	}
}
