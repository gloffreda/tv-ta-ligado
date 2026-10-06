package facts

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/llm"
)

// Source é o texto de origem de onde se extraem fatos.
type Source struct {
	ArticleID   int64
	Title       string
	Summary     string
	Body        string
	URL         string
	Credit      string
	PublishedAt time.Time
}

func (s Source) Text() string {
	parts := []string{s.Title, s.Summary}
	if s.Body != "" {
		parts = append(parts, s.Body)
	}
	return strings.Join(parts, "\n\n")
}

var extractionSchema = llm.MustSchema("extract.json", ExtractionSchema)

const extractSystem = `Você extrai fatos atômicos de notícias brasileiras para um telejornal que só pode afirmar o que está na fonte.

Regras:
- Use SOMENTE o texto fornecido. Não acrescente contexto, causas nem conhecimento próprio.
- Cada fato é uma afirmação verificável, curta, em português, que possa ser lida no ar.
- Copie números, datas e valores exatamente como aparecem no texto (mesmo formato, mesmas casas decimais).
- "entities": todas as pessoas, organizações, siglas e lugares citados no fato, escritos exatamente como no texto, cada um com "type": "person" (pessoa real), "org" (organização, órgão, empresa, partido, time), "place" (lugar) ou "other" (índice, evento, programa, produto). Palavras comuns em minúsculas não são entidades.
- "value": o principal número do fato, como número JSON (ex.: 5,43 vira 5.43; 2 bilhões vira 2000000000); null se não houver.
- "unit": unidade do value ("%", "BRL", "USD", "pessoas"...) ou "".
- NÃO expanda siglas nem abreviações (se o texto diz "MA", escreva "MA", nunca "Maranhão").
- NÃO calcule nada: nada de somas, totais, diferenças ou porcentagens que o texto não traz escritos.
- "sensitive": true se o fato envolve morte, violência, crime violento, desastre, acidente grave ou doença; senão false.
- No máximo 4 fatos, priorizando os mais importantes.

Responda apenas com JSON: {"facts":[{"claim":"...","entities":[{"name":"...","type":"person"}],"value":null,"unit":"","sensitive":false}]}`

type Extractor struct {
	LLM   llm.Client
	Model string
	Now   func() time.Time
}

// Extract pede fatos ao LLM e descarta, por código, os que não batem com a fonte.
func (e *Extractor) Extract(ctx context.Context, src Source) ([]Fact, []string, error) {
	text := src.Text()
	resp, err := e.LLM.Complete(ctx, llm.Request{
		Purpose: "extract", Model: e.Model, System: extractSystem,
		Prompt:    fmt.Sprintf("Fonte: %s\n\nTexto:\n%s", src.Credit, text),
		MaxTokens: 2000, Temperature: llm.Float(0),
	})
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		Facts []Candidate `json:"facts"`
	}
	if err := extractionSchema.Decode(resp.Text, &out); err != nil {
		return nil, nil, err
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	asOf := src.PublishedAt
	if asOf.IsZero() {
		asOf = now
	}
	var kept []Fact
	var discarded []string
	for _, c := range out.Facts {
		if err := Validate(c, text); err != nil {
			discarded = append(discarded, fmt.Sprintf("%q: %v", c.Claim, err))
			slog.Debug("fato descartado", "claim", c.Claim, "motivo", err)
			continue
		}
		id := src.ArticleID
		kept = append(kept, Fact{
			ArticleID: &id, Kind: Headline, Claim: strings.TrimSpace(c.Claim), Entities: c.Entities,
			Value: c.Value, Unit: c.Unit, AsOf: asOf, SourceName: src.Credit, SourceURL: src.URL, Sensitive: c.Sensitive,
			ExpiresAt: asOf.Add(TTL(Headline)),
		})
	}
	return kept, discarded, nil
}
