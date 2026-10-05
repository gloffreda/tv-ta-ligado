package facts

import "testing"

func f64(v float64) *float64 { return &v }

func TestValidate(t *testing.T) {
	src := "O ministro Fernando Haddad disse nesta segunda-feira (5) que a arrecadação federal somou R$ 210,5 bilhões em setembro, alta de 3,2% sobre 2025."
	cases := []struct {
		name string
		c    Candidate
		ok   bool
	}{
		{"ok", Candidate{Claim: "A arrecadação federal somou R$ 210,5 bilhões em setembro, segundo Fernando Haddad.", Entities: []string{"Fernando Haddad"}, Value: f64(210.5e9)}, true},
		{"percentual ok", Candidate{Claim: "A arrecadação teve alta de 3,2% sobre 2025.", Value: f64(3.2), Unit: "%"}, true},
		{"número inventado", Candidate{Claim: "A arrecadação somou R$ 215 bilhões.", Entities: nil}, false},
		{"número arredondado", Candidate{Claim: "A arrecadação teve alta de 3% sobre 2025."}, false},
		{"value fora da fonte", Candidate{Claim: "A arrecadação federal cresceu.", Value: f64(4.1)}, false},
		{"entidade fora da fonte", Candidate{Claim: "Lula comentou a arrecadação.", Entities: []string{"Lula"}}, false},
		{"entidade com acento diferente", Candidate{Claim: "Haddad falou sobre arrecadação.", Entities: []string{"fernando haddad"}}, true},
	}
	for _, c := range cases {
		err := Validate(c.c, src)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestFormatBR(t *testing.T) {
	for in, want := range map[float64]string{1234.56: "1.234,56", 5.2079: "5,2079", -0.32: "-0,32", 1234567: "1.234.567,00"} {
		dec := 2
		if in == 5.2079 {
			dec = 4
		}
		if got := FormatBR(in, dec); got != want {
			t.Errorf("FormatBR(%v)=%q want %q", in, got, want)
		}
	}
}
