package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

var t0 = time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)

// Item com 3 falas: [0,2000) orlando · pausa 500 · [2500,4000) duda · pausa 350 · [4350,5350) duda.
func seed(t *testing.T) (*store.Store, int64) {
	st := testfix.DB(t, "api")
	ctx := context.Background()
	for _, h := range []string{"a1", "a2", "a3"} {
		st.InsertAudioAsset(ctx, store.AudioAsset{Hash: strings.Repeat(h, 32), Path: "/dev/null", DurationMS: 1, Provider: "fake", Voice: "v", SpokenText: "x", Visemes: json.RawMessage(`[{"start_ms":0,"end_ms":100,"shape":"B"}]`)})
	}
	id, err := st.InsertTimelineItem(ctx, store.TimelineItem{Kind: "bumper", StartsAt: t0, EndsAt: t0.Add(6 * time.Second), Lines: []store.TimelineLine{
		{Seq: 1, Speaker: "orlando", Type: "banter", Text: "Boa noite.", SpokenText: "Boa noite.", AudioHash: strings.Repeat("a1", 32), OffsetMS: 0, DurationMS: 2000},
		{Seq: 2, Speaker: "duda", Type: "banter", Text: "Oi!", SpokenText: "Oi!", AudioHash: strings.Repeat("a2", 32), OffsetMS: 2500, DurationMS: 1500},
		{Seq: 3, Speaker: "duda", Type: "banter", Text: "Tá ligado?", SpokenText: "Tá ligado?", AudioHash: strings.Repeat("a3", 32), OffsetMS: 4350, DurationMS: 1000},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return st, id
}

func TestNowFiveInstants(t *testing.T) {
	st, id := seed(t)
	s := &Server{Store: st}
	cases := []struct {
		at       time.Duration
		line     int // 0 = pausa
		pos      int
		next     int
		nextInMS int
	}{
		{0, 1, 0, 0, 0},                          // começo exato da primeira fala
		{1999 * time.Millisecond, 1, 1999, 0, 0}, // último ms da fala 1
		{2000 * time.Millisecond, 0, 0, 2, 500},  // troca entre falas: pausa
		{2500 * time.Millisecond, 2, 0, 0, 0},    // começo da fala 2
		{4700 * time.Millisecond, 3, 350, 0, 0},  // meio da fala 3
	}
	for _, c := range cases {
		n, err := s.At(context.Background(), t0.Add(c.at))
		if err != nil || n.Item == nil || n.Item.ID != id {
			t.Fatalf("%v: %+v %v", c.at, n, err)
		}
		switch {
		case c.line == 0:
			if n.Line != nil || n.NextLine == nil || n.NextLine.Seq != c.next || n.NextLineInMS != c.nextInMS {
				t.Errorf("%v: esperava pausa antes da fala %d em %d ms: %+v", c.at, c.next, c.nextInMS, n)
			}
		default:
			if n.Line == nil || n.Line.Seq != c.line || n.PositionMS != c.pos {
				t.Errorf("%v: fala %d pos %d; got %+v pos %d", c.at, c.line, c.pos, n.Line, n.PositionMS)
			}
		}
		if n.ItemPosMS != int(c.at.Milliseconds()) {
			t.Errorf("%v: posição no item %d", c.at, n.ItemPosMS)
		}
	}
	// Fora de qualquer item.
	if n, _ := s.At(context.Background(), t0.Add(time.Hour)); n.Item != nil {
		t.Fatal("não há item no ar")
	}
}

func TestHTTPEndpoints(t *testing.T) {
	st, _ := seed(t)
	media := t.TempDir()
	hash := strings.Repeat("a1", 32)
	os.WriteFile(filepath.Join(media, hash+".ogg"), []byte("OggS"), 0o644)
	s := &Server{Store: st, MediaDir: media, Now: func() time.Time { return t0.Add(2600 * time.Millisecond) }}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var now Now
	resp, _ := http.Get(srv.URL + "/v1/now")
	json.NewDecoder(resp.Body).Decode(&now)
	if now.Line == nil || now.Line.Seq != 2 || now.PositionMS != 100 || !strings.HasPrefix(now.Line.AudioURL, "/media/") {
		t.Fatalf("/v1/now: %+v", now)
	}
	var tl struct{ Items []Item }
	resp, _ = http.Get(srv.URL + "/v1/timeline?from=" + t0.Add(-time.Minute).Format(time.RFC3339) + "&to=" + t0.Add(time.Minute).Format(time.RFC3339))
	json.NewDecoder(resp.Body).Decode(&tl)
	if len(tl.Items) != 1 || len(tl.Items[0].Lines) != 3 || string(tl.Items[0].Lines[0].Visemes) == "" || !tl.Items[0].Lines[1].StartsAt.Equal(t0.Add(2500*time.Millisecond)) {
		t.Fatalf("/v1/timeline: %+v", tl)
	}
	resp, _ = http.Get(srv.URL + "/media/" + hash + ".ogg")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("/media: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := http.Get(srv.URL + "/media/../../etc/passwd"); resp.StatusCode == 200 {
		t.Fatal("path traversal")
	}
	if resp, _ := http.Get(srv.URL + "/healthz"); resp.StatusCode != 200 {
		t.Fatal("/healthz")
	}
}

func TestSSE(t *testing.T) {
	st, id := seed(t)
	start := time.Now()
	// Relógio simulado que começa 100 ms antes da fala 2 e corre em tempo real.
	s := &Server{Store: st, Tick: 20 * time.Millisecond, Now: func() time.Time { return t0.Add(2400*time.Millisecond + time.Since(start)) }}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64<<10)
	var got strings.Builder
	for !strings.Contains(got.String(), `"line":{"seq":3`) {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
	}
	out := got.String()
	if !strings.Contains(out, "event: item_started") || !strings.Contains(out, "event: line_started") || !strings.Contains(out, `"line":{"seq":2`) {
		t.Fatalf("SSE: %s", out)
	}
	_ = id
}
