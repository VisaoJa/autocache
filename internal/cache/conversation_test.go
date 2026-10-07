package cache

import (
	"encoding/json"
	"strings"
	"testing"

	"autocache/internal/types"
)

// Pedido no formato do laço de ferramentas do n8n (Vitória):
// system em blocos + tools + histórico + tool_use + tool_result.
const reqLaco = `{"model":"claude-sonnet-5-5","max_tokens":100,
 "system":[{"type":"text","text":"sys"}],
 "tools":[{"name":"t","description":"d","input_schema":{"type":"object","properties":{}}}],
 "messages":[
  {"role":"user","content":"oi"},
  {"role":"assistant","content":"ola"},
  {"role":"user","content":"quero agendar"},
  {"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"sig"},{"type":"tool_use","id":"tu1","name":"t","input":{}}]},
  {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"r"}]}
 ]}`

func parse(t *testing.T, s string) *types.AnthropicRequest {
	t.Helper()
	var r types.AnthropicRequest
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &r
}

func TestConversationBreakpointNoToolResult(t *testing.T) {
	t.Setenv("CACHE_CONVERSATION", "")
	r := parse(t, reqLaco)
	bp, ok := ApplyConversationBreakpoint(r)
	if !ok || bp.Type != "conversation" || bp.TTL != "5m" {
		t.Fatalf("esperava breakpoint de conversa, veio ok=%v bp=%+v", ok, bp)
	}
	last := r.Messages[len(r.Messages)-1].Content
	if last[len(last)-1].CacheControl == nil || last[len(last)-1].Type != "tool_result" {
		t.Fatalf("cache_control não foi para o tool_result final: %+v", last)
	}
	// thinking nunca recebe cache_control
	for _, b := range r.Messages[3].Content {
		if b.Type == "thinking" && b.CacheControl != nil {
			t.Fatal("cache_control em bloco thinking")
		}
	}
	out, _ := json.Marshal(r)
	if !strings.Contains(string(out), `"tool_use_id":"tu1","content":"r","cache_control":{"type":"ephemeral","ttl":"5m"}`) &&
		!strings.Contains(string(out), `"cache_control":{"type":"ephemeral","ttl":"5m"}`) {
		t.Fatalf("cache_control não serializado: %s", out)
	}
}

func TestConversationBreakpointTextoSimples(t *testing.T) {
	t.Setenv("CACHE_CONVERSATION", "")
	r := parse(t, `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"oi"}]}`)
	if _, ok := ApplyConversationBreakpoint(r); !ok {
		t.Fatal("deveria marcar a mensagem de texto")
	}
	if r.Messages[0].Content[0].CacheControl == nil {
		t.Fatal("texto sem cache_control")
	}
}

func TestConversationBreakpointDesligado(t *testing.T) {
	t.Setenv("CACHE_CONVERSATION", "false")
	r := parse(t, reqLaco)
	if _, ok := ApplyConversationBreakpoint(r); ok {
		t.Fatal("CACHE_CONVERSATION=false deveria desligar")
	}
	out, _ := json.Marshal(r)
	if strings.Contains(string(out), "cache_control") {
		t.Fatalf("não deveria ter cache_control: %s", out)
	}
}

func TestConversationBreakpointRespeitaClienteELimite(t *testing.T) {
	t.Setenv("CACHE_CONVERSATION", "")
	// cliente já marcou uma mensagem: não mexe
	r := parse(t, `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":"b"}]}`)
	if _, ok := ApplyConversationBreakpoint(r); ok {
		t.Fatal("não deveria marcar quando o cliente já marcou mensagens")
	}
	// já há 4 breakpoints (limite da API): não adiciona o 5º
	r2 := parse(t, `{"model":"m","max_tokens":1,
	 "system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}},{"type":"text","text":"b","cache_control":{"type":"ephemeral"}}],
	 "tools":[{"name":"t1","input_schema":{},"cache_control":{"type":"ephemeral"}},{"name":"t2","input_schema":{},"cache_control":{"type":"ephemeral"}}],
	 "messages":[{"role":"user","content":"oi"}]}`)
	if _, ok := ApplyConversationBreakpoint(r2); ok {
		t.Fatal("não pode passar de 4 breakpoints")
	}
}

func TestConversationBreakpointUltimoSoThinking(t *testing.T) {
	t.Setenv("CACHE_CONVERSATION", "")
	r := parse(t, `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"oi"},{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"s"}]}]}`)
	if _, ok := ApplyConversationBreakpoint(r); ok {
		t.Fatal("última mensagem só com thinking não pode receber cache_control")
	}
}
