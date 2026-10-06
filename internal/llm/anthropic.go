package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic é a implementação real de Client.
type Anthropic struct{ c anthropic.Client }

func NewAnthropic(apiKey string) *Anthropic {
	return &Anthropic{c: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithMaxRetries(3))}
}

// SupportsTemperature: modelos novos (Sonnet 5.x, Opus 4.7+, Fable) rejeitam
// temperatura diferente do padrão; nesses ela é omitida.
func SupportsTemperature(model string) bool {
	for _, p := range []string{"claude-sonnet-5", "claude-opus-5", "claude-opus-4-7", "claude-opus-4-8", "claude-fable", "claude-mythos"} {
		if strings.HasPrefix(model, p) {
			return false
		}
	}
	return true
}

func supportsEffort(model string) bool { return !strings.HasPrefix(model, "claude-haiku") }

func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt))},
	}
	if req.System != "" {
		block := anthropic.TextBlockParam{Text: req.System}
		if req.CacheSystem {
			block.CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
		params.System = []anthropic.TextBlockParam{block}
	}
	if req.Temperature != nil && SupportsTemperature(req.Model) {
		params.Temperature = anthropic.Float(*req.Temperature)
	}
	if req.Effort != "" && supportsEffort(req.Model) {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(req.Effort)}
	}
	msg, err := a.c.Messages.New(ctx, params)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic (%s): %w", req.Purpose, err)
	}
	r := Response{
		Model:            string(msg.Model),
		InputTokens:      int(msg.Usage.InputTokens),
		OutputTokens:     int(msg.Usage.OutputTokens),
		CacheReadTokens:  int(msg.Usage.CacheReadInputTokens),
		CacheWriteTokens: int(msg.Usage.CacheCreationInputTokens),
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return r, ErrRefused
	}
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	r.Text = b.String()
	if msg.StopReason == anthropic.StopReasonMaxTokens {
		return r, fmt.Errorf("resposta truncada (max_tokens) em %s", req.Purpose)
	}
	return r, nil
}
