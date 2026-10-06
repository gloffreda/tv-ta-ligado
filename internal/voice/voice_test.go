package voice

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/audio"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/lipsync"
	"github.com/gloffreda/tv-ta-ligado/internal/speech"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
	"github.com/gloffreda/tv-ta-ligado/internal/tts"
)

// fakeProvider devolve um tom de 60 ms por caractere; fail faz falhar.
type fakeProvider struct {
	name  string
	calls int32
	fail  bool
	price float64
}

func (f *fakeProvider) Name() string           { return f.name }
func (f *fakeProvider) PricePerMChar() float64 { return f.price }
func (f *fakeProvider) Synthesize(_ context.Context, text string, _ tts.Voice) (tts.Audio, time.Duration, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.fail {
		return tts.Audio{}, 0, errors.New("nuvem fora do ar")
	}
	d := time.Duration(len(text)) * 60 * time.Millisecond
	return tts.Audio{Data: audio.Tone(d), Format: "wav", SampleRate: audio.SampleRate}, d, nil
}

type fakeLip struct{}

func (fakeLip) Visemes(_ context.Context, wav []byte) ([]lipsync.Cue, error) {
	_, d, _, err := tts.WAVInfo(wav)
	return []lipsync.Cue{{StartMS: 0, EndMS: int(d.Milliseconds()), Shape: "B"}}, err
}

type ledger struct {
	*store.Store
	tts int32
}

func newVoicer(t *testing.T, st *store.Store, ps map[string]tts.Provider, fallback []string) *Voicer {
	d, err := speech.LoadDict(testfix.Path("config", "pronunciation.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return &Voicer{Store: st, Router: &tts.Router{Providers: ps, Fallback: fallback}, Lip: fakeLip{}, Proc: audio.Pure{},
		MediaDir: t.TempDir(), Dict: d, Ledger: st}
}

func TestCacheSameLineNoResynthesis(t *testing.T) {
	st := testfix.DB(t, "voice")
	local := &fakeProvider{name: "kokoro"}
	v := newVoicer(t, st, map[string]tts.Provider{"kokoro": local}, nil)
	voiceCfg := tts.Voice{Provider: "kokoro", Name: "pm_santa", Rate: 0.92, Pitch: -1}
	ctx := context.Background()
	spoken := v.Spoken(ctx, "O dólar fechou a R$ 5,43.")
	if spoken != "O dólar fechou a cinco reais e quarenta e três centavos." {
		t.Fatalf("spoken=%q", spoken)
	}
	a1, cached1, err := v.Asset(ctx, voiceCfg, nil, spoken, nil)
	if err != nil || cached1 {
		t.Fatalf("1ª síntese: cached=%v err=%v", cached1, err)
	}
	a2, cached2, err := v.Asset(ctx, voiceCfg, nil, spoken, nil)
	if err != nil || !cached2 || a2.Hash != a1.Hash || a2.Path != a1.Path {
		t.Fatalf("mesma fala deve vir do cache: %+v cached=%v err=%v", a2, cached2, err)
	}
	if local.calls != 1 {
		t.Fatalf("sintetizou %d vezes; esperado 1", local.calls)
	}
	// Voz diferente (outro rate) é outro arquivo.
	other := voiceCfg
	other.Rate = 1.0
	if a3, _, _ := v.Asset(ctx, other, nil, spoken, nil); a3.Hash == a1.Hash {
		t.Fatal("parâmetros de voz diferentes não podem compartilhar áudio")
	}
	var calls int
	_ = st.DB.QueryRow(ctx, `SELECT count(*) FROM llm_calls WHERE purpose='tts'`).Scan(&calls)
	if calls != 2 {
		t.Fatalf("custo de TTS registrado %d vezes; esperado 2 (cache não cobra)", calls)
	}
	if a1.DurationMS != len(spoken)*60 {
		t.Fatalf("duração %d ms", a1.DurationMS)
	}
}

func TestCloudFallsBackToLocalAfter3Failures(t *testing.T) {
	st := testfix.DB(t, "voice_fallback")
	cloud := &fakeProvider{name: "azure", fail: true, price: 15}
	local := &fakeProvider{name: "kokoro"}
	v := newVoicer(t, st, map[string]tts.Provider{"azure": cloud, "kokoro": local}, []string{"kokoro", "piper"})
	var events []string
	v.Router.OnEvent = func(kind string, _ map[string]any) { events = append(events, kind) }
	primary := tts.Voice{Provider: "azure", Name: "pt-BR-AntonioNeural"}
	fb := map[string]tts.Voice{"kokoro": {Provider: "kokoro", Name: "pm_santa"}}
	a, _, err := v.Asset(context.Background(), primary, fb, "Boa noite.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Provider != "kokoro" || cloud.calls != 3 || local.calls != 1 {
		t.Fatalf("provedor=%s nuvem=%d local=%d", a.Provider, cloud.calls, local.calls)
	}
	if len(events) != 1 || events[0] != "tts_fallback" {
		t.Fatalf("aviso de fallback: %v", events)
	}
	// Rebaixada, a nuvem nem é tentada na próxima fala.
	if _, _, err := v.Asset(context.Background(), primary, fb, "Até já.", nil); err != nil || cloud.calls != 3 {
		t.Fatalf("nuvem rebaixada não deveria ser chamada: %d %v", cloud.calls, err)
	}
}

func TestVoiceSegment(t *testing.T) {
	st := testfix.DB(t, "voice_segment")
	ctx := context.Background()
	rid, _ := st.CreateRundown(ctx, "noticias", time.Now())
	sid, _ := st.CreateSegment(ctx, rid, "noticias", time.Now())
	texts := []struct{ sp, typ, text, status string }{
		{"orlando", "fact", "O dólar fechou a R$ 5,43 em 02/10/2026.", "ok"},
		{"duda", "banter", "Tá ligado?", "rewritten"},
		{"orlando", "fact", "Texto cortado.", "dropped"},
	}
	for i, l := range texts {
		id, err := st.InsertLine(ctx, store.Line{SegmentID: sid, Seq: i + 1, Speaker: l.sp, Type: l.typ, Text: l.text})
		if err != nil {
			t.Fatal(err)
		}
		_ = st.UpdateLine(ctx, id, l.status, l.text, nil, "", nil)
	}
	_ = st.FinishSegment(ctx, sid, "approved", "")
	local := &fakeProvider{name: "kokoro"}
	v := newVoicer(t, st, map[string]tts.Provider{"kokoro": local}, nil)
	personas := map[string]config.Persona{
		"orlando": {Voice: tts.Voice{Provider: "kokoro", Name: "pm_santa"}},
		"duda":    {Voice: tts.Voice{Provider: "kokoro", Name: "pf_dora"}},
	}
	n, err := v.VoiceSegment(ctx, sid, personas)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v (fala cortada não é sintetizada)", n, err)
	}
	var text, spoken string
	_ = st.DB.QueryRow(ctx, `SELECT text, spoken_text FROM lines WHERE segment_id=$1 AND seq=1`, sid).Scan(&text, &spoken)
	if text != texts[0].text || !strings.Contains(spoken, "cinco reais e quarenta e três centavos") || !strings.Contains(spoken, "dois de outubro") {
		t.Fatalf("text deve ficar intacto e spoken normalizado: %q / %q", text, spoken)
	}
	if err := speech.VerifyNumbers(text, spoken); err != nil {
		t.Fatal(err)
	}
	if n2, _ := v.VoiceSegment(ctx, sid, personas); n2 != 0 || local.calls != 2 {
		t.Fatalf("segundo passe não pode ressintetizar: n=%d calls=%d", n2, local.calls)
	}
}
