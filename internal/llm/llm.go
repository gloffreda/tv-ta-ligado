// Package llm isola todo acesso a modelos de linguagem atrás de Client,
// com implementação real (Anthropic), mock (testes offline) e contabilidade.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

type Request struct {
	Purpose     string // rundown | extract | script | rewrite | judge | memory
	Model       string
	System      string
	Prompt      string
	MaxTokens   int
	Temperature *float64 // só enviado a modelos que aceitam
	Effort      string   // low|medium|high (modelos com adaptive thinking)
}

type Response struct {
	Text         string
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64 // preenchido pelo Metered
}

type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// ErrNoAPIKey: não há credencial; nenhuma chamada é possível.
var ErrNoAPIKey = errors.New("ANTHROPIC_API_KEY ausente")

// ErrRefused: o modelo recusou responder (tratado como reprovação).
var ErrRefused = errors.New("modelo recusou a solicitação")

type ctxKey struct{}

// WithSegment associa as chamadas seguintes a um segmento (custo por segmento).
func WithSegment(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func SegmentFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKey{}).(int64)
	return id, ok
}

// Float é um atalho para Temperature.
func Float(v float64) *float64 { return &v }

// ExtractJSON tira cercas de código e devolve o primeiro objeto JSON do texto.
func ExtractJSON(text string) (string, error) {
	t := strings.TrimSpace(text)
	t = strings.TrimPrefix(t, "```json")
	t = strings.TrimPrefix(t, "```")
	t = strings.TrimSuffix(t, "```")
	i := strings.Index(t, "{")
	j := strings.LastIndex(t, "}")
	if i < 0 || j < i {
		return "", errors.New("resposta sem objeto JSON")
	}
	return t[i : j+1], nil
}

// Schema é um JSON Schema compilado.
type Schema struct{ s *jsonschema.Schema }

func MustSchema(name, src string) *Schema {
	s, err := jsonschema.CompileString(name, src)
	if err != nil {
		panic(fmt.Sprintf("schema %s: %v", name, err))
	}
	return &Schema{s}
}

// Decode extrai o JSON do texto, valida contra o schema e decodifica em out.
func (sc *Schema) Decode(text string, out any) error {
	raw, err := ExtractJSON(text)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("JSON inválido: %w", err)
	}
	if err := sc.s.Validate(v); err != nil {
		return fmt.Errorf("JSON fora do schema: %v", err)
	}
	d2 := json.NewDecoder(bytes.NewReader([]byte(raw)))
	return d2.Decode(out)
}

// Fatal: erros que devem interromper a geração inteira (teto de orçamento,
// contexto cancelado), em vez de só reprovar uma fala.
func Fatal(err error) bool {
	return errors.Is(err, ErrBudget) || errors.Is(err, ErrNoAPIKey) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
