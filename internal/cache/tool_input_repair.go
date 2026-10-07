package cache

import (
	"os"
	"strings"
	"sync"
	"time"

	"autocache/internal/types"
)

// VisaoJa 07/10/2026 — reparo do "input" dos tool_use reenviados pelo n8n.
//
// Sintoma (amostra de 49 execuções da Vitória): o AI Agent v3 do n8n reenvia
// os tool_use anteriores do MESMO laço com "input": {} (perde os argumentos).
// O modelo vê "chamei registrar_lead com {} → sucesso" e chama de novo
// (registrar_lead ×4–5, verificar_bloqueio ×3...), ~22% das chamadas.
//
// O proxy vê as respostas da API (com o input real de cada tool_use, por id).
// Guardamos id → input por alguns minutos e, nos pedidos seguintes, recolocamos
// o input real nos tool_use que chegarem vazios. Nada é inventado: só
// devolvemos ao histórico o que o próprio modelo enviou.
//
// TOOL_INPUT_REPAIR=true liga o reparo. Desligado (padrão), só CONTA e loga
// (auditoria), sem alterar nada.

const (
	toolInputTTL     = 15 * time.Minute
	toolInputMaxSize = 20000
)

type toolInputEntry struct {
	input interface{}
	at    time.Time
}

type ToolInputStore struct {
	mu sync.Mutex
	m  map[string]toolInputEntry
}

func NewToolInputStore() *ToolInputStore {
	return &ToolInputStore{m: make(map[string]toolInputEntry)}
}

func ToolInputRepairEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("TOOL_INPUT_REPAIR")))
	return v == "true" || v == "1" || v == "on"
}

func isEmptyToolInput(v interface{}) bool {
	if v == nil {
		return true
	}
	if m, ok := v.(map[string]interface{}); ok {
		return len(m) == 0
	}
	return false
}

// Remember guarda o input de cada tool_use de uma resposta da API.
func (s *ToolInputStore) Remember(resp *types.AnthropicResponse) int {
	if s == nil || resp == nil {
		return 0
	}
	now := time.Now()
	n := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range resp.Content {
		if b.Type != "tool_use" || b.ID == "" || isEmptyToolInput(b.Input) {
			continue
		}
		s.m[b.ID] = toolInputEntry{input: b.Input, at: now}
		n++
	}
	if len(s.m) > toolInputMaxSize {
		s.pruneLocked(now)
	}
	return n
}

func (s *ToolInputStore) pruneLocked(now time.Time) {
	for k, e := range s.m {
		if now.Sub(e.at) > toolInputTTL {
			delete(s.m, k)
		}
	}
	// ainda grande demais (tráfego anormal): esvazia — pior caso = comportamento antigo
	if len(s.m) > toolInputMaxSize {
		s.m = make(map[string]toolInputEntry)
	}
}

func (s *ToolInputStore) lookup(id string, now time.Time) (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok || now.Sub(e.at) > toolInputTTL {
		return nil, false
	}
	return e.input, true
}

// ToolInputAudit resume o que foi visto/reparado num pedido.
type ToolInputAudit struct {
	ToolUses int // tool_use em mensagens do assistente
	Empty    int // com input vazio
	Known    int // vazios cujo input real o proxy conhece
	Repaired int // efetivamente reparados (só com TOOL_INPUT_REPAIR=true)
}

// Repair conta os tool_use vazios e, se repair=true, recoloca o input real.
func (s *ToolInputStore) Repair(req *types.AnthropicRequest, repair bool) ToolInputAudit {
	var a ToolInputAudit
	if s == nil || req == nil {
		return a
	}
	now := time.Now()
	for mi := range req.Messages {
		if req.Messages[mi].Role != "assistant" {
			continue
		}
		for bi := range req.Messages[mi].Content {
			b := &req.Messages[mi].Content[bi]
			if b.Type != "tool_use" {
				continue
			}
			a.ToolUses++
			if !isEmptyToolInput(b.Input) {
				continue
			}
			a.Empty++
			in, ok := s.lookup(b.ID, now)
			if !ok {
				continue
			}
			a.Known++
			if repair {
				b.Input = in
				a.Repaired++
			}
		}
	}
	return a
}
