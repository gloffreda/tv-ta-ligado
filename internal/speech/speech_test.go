package speech

import (
	"testing"

	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

func dict(t *testing.T) Dict {
	d, err := LoadDict(testfix.Path("config", "pronunciation.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNormalize(t *testing.T) {
	d := dict(t)
	cases := []struct{ in, want string }{
		{"O dólar fechou a R$ 5,43.", "O dólar fechou a cinco reais e quarenta e três centavos."},
		{"R$ 1,00 por litro", "um real por litro"},
		{"R$ 1,01", "um real e um centavo"},
		{"R$ 0,50", "cinquenta centavos"},
		{"R$ 4,9859", "quatro vírgula nove oito cinco nove reais"},
		{"R$ 48,6 milhões", "quarenta e oito vírgula seis milhões de reais"},
		{"R$ 2,5 mil", "dois vírgula cinco mil reais"},
		{"US$ 1,2 bilhão", "um vírgula dois bilhão de dólares"},
		{"US$ 1", "um dólar"},
		{"€ 300", "trezentos euros"},
		{"inflação de 5,2%", "inflação de cinco vírgula dois por cento"},
		{"variou -0,32%", "variou menos zero vírgula trinta e dois por cento"},
		{"alta de 0,05%", "alta de zero vírgula zero cinco por cento"},
		{"13,75% ao ano", "treze vírgula setenta e cinco por cento ao ano"},
		{"100%", "cem por cento"},
		{"caiu 1,5 ponto percentual", "caiu um vírgula cinco ponto percentual"},
		{"em 02/10/2026", "em dois de outubro de dois mil e vinte e seis"},
		{"em 01/08/2026", "em primeiro de agosto de dois mil e vinte e seis"},
		{"até 5/10", "até cinco de outubro"},
		{"2026-10-05", "cinco de outubro de dois mil e vinte e seis"},
		{"às 20h", "às vinte horas"},
		{"às 14h30", "às catorze horas e trinta minutos"},
		{"às 9:05", "às nove horas e cinco minutos"},
		{"às 1h", "à uma hora"},
		{"nesta segunda-feira (5)", "nesta segunda-feira, dia cinco"},
		{"no domingo (4)", "no domingo, dia quatro"},
		{"a 24ª mostra", "a vigésima quarta mostra"},
		{"o 1º lugar", "o primeiro lugar"},
		{"máxima de 27,4 °C", "máxima de vinte e sete vírgula quatro graus"},
		{"mínima de 18 °C", "mínima de dezoito graus"},
		{"1.250 metros", "mil duzentos e cinquenta metros"},
		{"34.869 votos", "trinta e quatro mil oitocentos e sessenta e nove votos"},
		{"2.500 trabalhadores", "dois mil e quinhentos trabalhadores"},
		{"2 pessoas e 200 vagas", "duas pessoas e duzentas vagas"},
		{"1 milhão de visualizações", "um milhão de visualizações"},
		{"3,5 milhões de vagas", "três vírgula cinco milhões de vagas"},
		{"85 mil visitantes", "oitenta e cinco mil visitantes"},
		{"3 a 1", "três a um"},
		{"em 2026", "em dois mil e vinte e seis"},
		{"1.000.000 de pessoas", "um milhão de pessoas"},
		{"O IPCA subiu", "O í-pê-cê-á subiu"},
		{"A Selic, definida pelo Copom", "A sélic, definida pelo cópom"},
		{"decisão do STF", "decisão do esse-tê-efe"},
		{"segundo o IBGE", "segundo o í-bê-gê-é"},
		{"a covid-19 voltou", "a covid dezenove voltou"},
		{"Isso está checado? Então eu leio.", "Isso está checado? Então eu leio."},
		{"No Rio de Janeiro, máxima de 25,3 °C e 47% de chance de chuva.", "No Rio de Janeiro, máxima de vinte e cinco vírgula três graus e quarenta e sete por cento de chance de chuva."},
		{"O IPCA variou -0,32% em agosto de 2026.", "O í-pê-cê-á variou menos zero vírgula trinta e dois por cento em agosto de dois mil e vinte e seis."},
		{"Em Janeiro, perto de Março, nada de números.", "Em Janeiro, perto de Março, nada de números."},
	}
	for _, c := range cases {
		got := Normalize(c.in, d)
		if got != c.want {
			t.Errorf("Normalize(%q)\n got  %q\n want %q", c.in, got, c.want)
			continue
		}
		if err := VerifyNumbers(c.in, got); err != nil {
			t.Errorf("verificação %q → %q: %v", c.in, got, err)
		}
	}
	if len(cases) < 40 {
		t.Fatalf("precisa de pelo menos 40 casos, tem %d", len(cases))
	}
}

// O texto falado tem de dizer os mesmos números: perder ou trocar um número reprova.
func TestVerifyNumbersCatchesMismatch(t *testing.T) {
	bad := []struct{ text, spoken string }{
		{"O dólar fechou a R$ 5,43.", "O dólar fechou a cinco reais e quarenta e dois centavos."},
		{"variou -0,32%", "variou zero vírgula três dois por cento"},
		{"em 02/10/2026", "em dois de novembro de dois mil e vinte e seis"},
		{"1.250 metros", "mil e duzentos metros"},
		{"às 20h", "às vinte e uma horas"},
		{"R$ 48,6 milhões", "quarenta e oito vírgula seis mil reais"},
		{"Que dia bonito", "Que dia bonito, com dez graus"},
	}
	for _, c := range bad {
		if err := VerifyNumbers(c.text, c.spoken); err == nil {
			t.Errorf("deveria reprovar: %q → %q", c.text, c.spoken)
		}
	}
}

func TestCardinalOrdinal(t *testing.T) {
	for n, want := range map[int64]string{0: "zero", 16: "dezesseis", 100: "cem", 101: "cento e um", 1000: "mil", 1001: "mil e um",
		2026: "dois mil e vinte e seis", 1250: "mil duzentos e cinquenta", 1200: "mil e duzentos", 21000000: "vinte e um milhões",
		1000000000: "um bilhão", 999: "novecentos e noventa e nove"} {
		if got := Cardinal(n, false); got != want {
			t.Errorf("Cardinal(%d)=%q want %q", n, got, want)
		}
	}
	if Ordinal(24, true) != "vigésima quarta" || Ordinal(1, false) != "primeiro" || Ordinal(10, false) != "décimo" {
		t.Error("ordinais")
	}
}
