package llm

import (
	"context"
	"fmt"
	"sync"
)

// Mock responde com textos gravados, sem rede. Handler tem prioridade;
// sem ele, consome a fila de respostas do Purpose da requisição.
type Mock struct {
	mu        sync.Mutex
	Handler   func(Request) (string, error)
	Queues    map[string][]string
	Calls     []Request
	InTokens  int
	OutTokens int
}

func NewMock(queues map[string][]string) *Mock {
	return &Mock{Queues: queues, InTokens: 1000, OutTokens: 200}
}

func (m *Mock) Complete(_ context.Context, req Request) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, req)
	resp := Response{Model: req.Model, InputTokens: m.InTokens, OutputTokens: m.OutTokens}
	if m.Handler != nil {
		text, err := m.Handler(req)
		resp.Text = text
		return resp, err
	}
	q := m.Queues[req.Purpose]
	if len(q) == 0 {
		return resp, fmt.Errorf("mock: sem resposta para %q", req.Purpose)
	}
	resp.Text = q[0]
	m.Queues[req.Purpose] = q[1:]
	return resp, nil
}

// CallsFor conta as chamadas de um propósito.
func (m *Mock) CallsFor(purpose string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.Calls {
		if c.Purpose == purpose {
			n++
		}
	}
	return n
}
