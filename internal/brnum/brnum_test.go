package brnum

import (
	"math"
	"testing"
)

func TestExtract(t *testing.T) {
	type want struct {
		kind  Kind
		value float64
		dec   int
		neg   bool
		date  DateParts
	}
	cases := []struct {
		in   string
		want []want
	}{
		{"O dólar fechou a R$ 5,43.", []want{{BRL, 5.43, 2, false, DateParts{}}}},
		{"cotação de R$5,2079", []want{{BRL, 5.2079, 4, false, DateParts{}}}},
		{"inflação de 5,2% no ano", []want{{Percent, 5.2, 1, false, DateParts{}}}},
		{"IPCA de -0,32% em agosto", []want{{Percent, 0.32, 2, true, DateParts{}}}},
		{"alta de 0,5 por cento", []want{{Percent, 0.5, 1, false, DateParts{}}}},
		{"subiu 1,5 ponto percentual", []want{{Points, 1.5, 1, false, DateParts{}}}},
		{"queda de 2 pontos percentuais", []want{{Points, 2, 0, false, DateParts{}}}},
		{"1.234,56 toneladas", []want{{Plain, 1234.56, 2, false, DateParts{}}}},
		{"1.234.567 pessoas", []want{{Plain, 1234567, 0, false, DateParts{}}}},
		{"US$ 2 bilhões em exportações", []want{{USD, 2e9, 0, false, DateParts{}}}},
		{"R$ 1,2 milhão", []want{{BRL, 1.2e6, 1, false, DateParts{}}}},
		{"3,5 milhões de vagas", []want{{Plain, 3.5e6, 1, false, DateParts{}}}},
		{"R$ 2,5 mil por mês", []want{{BRL, 2500, 1, false, DateParts{}}}},
		{"R$ 4 bi no trimestre", []want{{BRL, 4e9, 0, false, DateParts{}}}},
		{"€ 300 de multa", []want{{EUR, 300, 0, false, DateParts{}}}},
		{"500 reais", []want{{BRL, 500, 0, false, DateParts{}}}},
		{"em 02/10/2026 o BC", []want{{Date, 0, 0, false, DateParts{2, 10, 2026}}}},
		{"no dia 5/10", []want{{Date, 0, 0, false, DateParts{5, 10, 0}}}},
		{"2026-10-05", []want{{Date, 0, 0, false, DateParts{5, 10, 2026}}}},
		{"em 5 de outubro de 2026", []want{{Date, 0, 0, false, DateParts{5, 10, 2026}}}},
		{"até 1º de março", []want{{Date, 0, 0, false, DateParts{1, 3, 0}}}},
		{"em agosto de 2026", []want{{Date, 0, 0, false, DateParts{0, 8, 2026}}}},
		{"às 14h30 de Brasília", []want{{Time, 14.30, 2, false, DateParts{}}}},
		{"às 9h", []want{{Time, 9, 2, false, DateParts{}}}},
		{"a Selic de 13,75% ao ano desde 2025", []want{{Percent, 13.75, 2, false, DateParts{}}, {Plain, 2025, 0, false, DateParts{}}}},
		{"três ministros e dez deputados", []want{{Word, 3, 0, false, DateParts{}}, {Word, 10, 0, false, DateParts{}}}},
		{"mil motivos", []want{{Word, 1000, 0, false, DateParts{}}}},
		{"taxa de 5.2 ao ano", []want{{Plain, 5.2, 1, false, DateParts{}}}},
		{"a covid-19 voltou", []want{{Plain, 19, 0, false, DateParts{}}}},
		{"máxima de 27 °C e 40% de chance de chuva", []want{{Plain, 27, 0, false, DateParts{}}, {Percent, 40, 0, false, DateParts{}}}},
		{"nenhum número aqui, só conversa", nil},
		{"o g1 informa", nil},
		{"a B3 fechou", nil},
		{"cúpula do G20", nil},
		{"a COP30 em Belém", nil},
		{"uma ideia e um café", nil},
		{"em dezembro, talvez", nil},
	}
	for _, c := range cases {
		got := Extract(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %d números %+v, want %d", c.in, len(got), got, len(c.want))
			continue
		}
		for i, w := range c.want {
			g := got[i]
			if g.Kind != w.kind {
				t.Errorf("%q[%d]: kind %s, want %s", c.in, i, g.Kind, w.kind)
			}
			if w.kind == Date {
				if g.D != w.date {
					t.Errorf("%q[%d]: date %+v, want %+v", c.in, i, g.D, w.date)
				}
				continue
			}
			if math.Abs(g.Value-w.value) > 1e-9 {
				t.Errorf("%q[%d]: value %v, want %v", c.in, i, g.Value, w.value)
			}
			if w.kind != Word && w.kind != Time && g.Decimals != w.dec {
				t.Errorf("%q[%d]: decimals %d, want %d", c.in, i, g.Decimals, w.dec)
			}
			if g.Negative != w.neg {
				t.Errorf("%q[%d]: negative %v, want %v", c.in, i, g.Negative, w.neg)
			}
		}
	}
	if len(cases) < 25 {
		t.Fatalf("a tabela precisa de pelo menos 25 casos, tem %d", len(cases))
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		line string
		fact float64
		ok   bool
	}{
		{"R$ 5,21", 5.2079, true},  // arredondamento correto
		{"R$ 5,20", 5.2079, false}, // truncado: errado
		{"R$ 5,2", 5.2079, true},
		{"R$ 5", 5.2079, true},
		{"R$ 5,2079", 5.2079, true},
		{"R$ 5,43", 5.2079, false}, // número ausente
		{"13,75%", 13.75, true},
		{"13,8%", 13.75, true},
		{"13,7%", 13.75, false},
		{"0,32%", -0.32, true},
		{"1,2 bilhão", 1234000000, true},
		{"1,3 bilhão", 1234000000, false},
		{"5.000.000", 5e6, true},
		{"5 milhões", 5e6, true},
	}
	for _, c := range cases {
		ns := Extract(c.line)
		if len(ns) != 1 {
			t.Fatalf("%q: esperava 1 número, veio %+v", c.line, ns)
		}
		if got := ns[0].Matches(c.fact); got != c.ok {
			t.Errorf("%q vs %v: got %v, want %v", c.line, c.fact, got, c.ok)
		}
	}
}
