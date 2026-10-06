package config

import (
	"testing"
	"time"
)

func TestProgramsAt(t *testing.T) {
	s, err := LoadSchedule("../../config")
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	cases := map[time.Time]string{
		time.Date(2026, 10, 6, 6, 55, 0, 0, loc):  "Tempo com Glória Garoa", // terça, inserção do tempo
		time.Date(2026, 10, 6, 7, 0, 0, 0, loc):   "Bom Dia, Tá Ligado",
		time.Date(2026, 10, 6, 21, 51, 0, 0, loc): "Tempo com Glória Garoa",
		time.Date(2026, 10, 6, 23, 59, 0, 0, loc): "Tá Ligado à Noite",
		time.Date(2026, 10, 10, 9, 0, 0, 0, loc):  "Fim de Semana Ligado", // sábado
	}
	for at, want := range cases {
		got, ok := s.Programs.At(at, loc)
		if !ok || got.Name != want {
			t.Errorf("%s: %q, quer %q", at, got.Name, want)
		}
	}
	if w, _ := s.Programs.At(time.Date(2026, 10, 6, 12, 50, 0, 0, loc), loc); w.Scene != "tempo" || w.Cast[0] != "gloria" {
		t.Errorf("tempo: cenário e elenco da Glória: %+v", w)
	}
	for _, b := range s.Blocks {
		if b.Data != "" && b.Name == "" {
			t.Error("bloco de dados sem nome")
		}
	}
}
