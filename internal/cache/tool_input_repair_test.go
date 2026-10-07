package cache

import (
	"encoding/json"
	"strings"
	"testing"

	"autocache/internal/types"
)

const respComTool = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","stop_reason":"tool_use",
 "content":[{"type":"thinking","thinking":"","signature":"sig"},
  {"type":"tool_use","id":"tu_A","name":"registrar_lead","input":{"status":"Novo Lead","cidade_interesse":"Itu"}},
  {"type":"tool_use","id":"tu_B","name":"Consultar_Cidade","input":{"cidade":"Itu"}}],
 "usage":{"input_tokens":1,"output_tokens":1}}`

// Formato que o n8n reenvia: cada tool_use numa mensagem do assistente, com input vazio.
const reqN8nVazio = `{"model":"claude-sonnet-5-5","max_tokens":100,"messages":[
 {"role":"user","content":"quero agendar em Itu"},
 {"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"tool_use","id":"tu_A","name":"registrar_lead","input":{}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_A","content":"ok"}]},
 {"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"tool_use","id":"tu_B","name":"Consultar_Cidade","input":{}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_B","content":"ok"}]},
 {"role":"assistant","content":[{"type":"tool_use","id":"tu_X","name":"desconhecido","input":{}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_X","content":"ok"}]}
]}`

func mustResp(t *testing.T) *types.AnthropicResponse {
	t.Helper()
	var r types.AnthropicResponse
	if err := json.Unmarshal([]byte(respComTool), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestToolInputAuditSemReparo(t *testing.T) {
	s := NewToolInputStore()
	if n := s.Remember(mustResp(t)); n != 2 {
		t.Fatalf("esperava guardar 2 inputs, guardou %d", n)
	}
	r := parse(t, reqN8nVazio)
	a := s.Repair(r, false)
	if a.ToolUses != 3 || a.Empty != 3 || a.Known != 2 || a.Repaired != 0 {
		t.Fatalf("auditoria errada: %+v", a)
	}
	out, _ := json.Marshal(r)
	if strings.Contains(string(out), `"cidade":"Itu"`) {
		t.Fatal("modo auditoria não pode alterar o pedido")
	}
}

func TestToolInputReparo(t *testing.T) {
	s := NewToolInputStore()
	s.Remember(mustResp(t))
	r := parse(t, reqN8nVazio)
	a := s.Repair(r, true)
	if a.Repaired != 2 {
		t.Fatalf("esperava 2 reparos: %+v", a)
	}
	out, _ := json.Marshal(r)
	o := string(out)
	if !strings.Contains(o, `"id":"tu_A","name":"registrar_lead","input":{"cidade_interesse":"Itu","status":"Novo Lead"}`) {
		t.Fatalf("tu_A não reparado: %s", o)
	}
	if !strings.Contains(o, `"id":"tu_B","name":"Consultar_Cidade","input":{"cidade":"Itu"}`) {
		t.Fatalf("tu_B não reparado: %s", o)
	}
	// desconhecido continua vazio (nada é inventado)
	if !strings.Contains(o, `"id":"tu_X","name":"desconhecido","input":{}`) {
		t.Fatalf("tu_X deveria seguir vazio: %s", o)
	}
	// thinking intacto
	if !strings.Contains(o, `{"type":"thinking","thinking":"","signature":"sig"}`) {
		t.Fatalf("thinking alterado: %s", o)
	}
}

func TestToolInputNaoMexeEmInputPreenchido(t *testing.T) {
	s := NewToolInputStore()
	s.Remember(mustResp(t))
	r := parse(t, `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"x"},
	 {"role":"assistant","content":[{"type":"tool_use","id":"tu_A","name":"registrar_lead","input":{"status":"Fila de Espera"}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_A","content":"ok"}]}]}`)
	a := s.Repair(r, true)
	if a.Empty != 0 || a.Repaired != 0 {
		t.Fatalf("não deveria reparar input preenchido: %+v", a)
	}
	out, _ := json.Marshal(r)
	if !strings.Contains(string(out), `"input":{"status":"Fila de Espera"}`) {
		t.Fatalf("input preenchido foi alterado: %s", out)
	}
}

func TestToolInputRepairEnv(t *testing.T) {
	t.Setenv("TOOL_INPUT_REPAIR", "")
	if ToolInputRepairEnabled() {
		t.Fatal("padrão deve ser desligado")
	}
	t.Setenv("TOOL_INPUT_REPAIR", "true")
	if !ToolInputRepairEnabled() {
		t.Fatal("true deve ligar")
	}
}
