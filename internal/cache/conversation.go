package cache

import (
	"os"
	"strings"
	"time"

	"autocache/internal/types"
)

// VisaoJa 07/10/2026 — cache do histórico da conversa.
//
// O injetor original só marca system e tools (mensagens só entram se UM bloco
// tiver >= 1024 tokens, o que nunca acontece num chat). Resultado: o histórico
// (~3k tokens por chamada) era pago a preço cheio em TODA volta do laço de
// ferramentas da mesma mensagem do paciente.
//
// Aqui colocamos um ponto de cache (5m) no último bloco cacheável da última
// mensagem. Na chamada seguinte do mesmo laço, todo o prefixo (tools + system +
// histórico + resultados anteriores) é lido do cache a 10% do preço.
// Cache não altera a resposta do modelo — só o preço da entrada.
//
// Desligar sem redeploy de código: CACHE_CONVERSATION=false.

const maxCacheBreakpoints = 4 // limite da API Anthropic

func conversationCachingEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("CACHE_CONVERSATION")))
	return v != "false" && v != "0" && v != "off"
}

// tipos de bloco que aceitam cache_control (thinking/redacted_thinking NÃO aceitam)
func blockAcceptsCacheControl(b *types.ContentBlock) bool {
	switch b.Type {
	case "text":
		return b.Text != ""
	case "tool_result", "tool_use", "image", "document":
		return true
	}
	return false
}

func countCacheControls(req *types.AnthropicRequest) (total int, inMessages int) {
	for i := range req.Tools {
		if req.Tools[i].CacheControl != nil {
			total++
		}
	}
	for i := range req.SystemBlocks {
		if req.SystemBlocks[i].CacheControl != nil {
			total++
		}
	}
	for mi := range req.Messages {
		for bi := range req.Messages[mi].Content {
			if req.Messages[mi].Content[bi].CacheControl != nil {
				total++
				inMessages++
			}
		}
	}
	return total, inMessages
}

// ApplyConversationBreakpoint marca o fim do histórico. Retorna o breakpoint
// aplicado e true, ou false se não aplicou (desligado, sem espaço, já marcado
// pelo cliente, ou nenhum bloco cacheável na última mensagem).
func ApplyConversationBreakpoint(req *types.AnthropicRequest) (types.CacheBreakpoint, bool) {
	if req == nil || len(req.Messages) == 0 || !conversationCachingEnabled() {
		return types.CacheBreakpoint{}, false
	}
	total, inMessages := countCacheControls(req)
	if inMessages > 0 || total >= maxCacheBreakpoints {
		return types.CacheBreakpoint{}, false
	}
	last := &req.Messages[len(req.Messages)-1]
	for bi := len(last.Content) - 1; bi >= 0; bi-- {
		b := &last.Content[bi]
		if !blockAcceptsCacheControl(b) {
			continue
		}
		b.CacheControl = &types.CacheControl{Type: "ephemeral", TTL: "5m"}
		return types.CacheBreakpoint{
			Position:  "conversation_tail",
			Tokens:    estimateMessagesTokens(req.Messages),
			TTL:       "5m",
			Type:      "conversation",
			Timestamp: time.Now(),
		}, true
	}
	return types.CacheBreakpoint{}, false
}

// Estimativa barata (~4 caracteres/token) só para métricas/log — não usa o
// tokenizer, que entra em pânico com prompts grandes.
func estimateMessagesTokens(msgs []types.Message) int {
	chars := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			chars += len(b.Text)
			if s, ok := b.Content.(string); ok {
				chars += len(s)
			}
		}
	}
	return chars / 4
}
