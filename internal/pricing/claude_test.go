package pricing

import (
	"github.com/zJay26/claude-usage/internal/model"
	"testing"
)

func TestClaudeCatalogAndProviderIdentifiers(t *testing.T) {
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-haiku-4-5@20251001"} {
		if !IsClaudeModel(id) {
			t.Fatal(id)
		}
		rate, ok, e := Resolve(id, nil)
		if !ok || e != nil || rate.CacheWrite1hNanoPerToken == nil {
			t.Fatalf("%s: %+v %v", id, rate, e)
		}
	}
	for _, id := range []string{"deepseek-flash", "<synthetic>", "gpt-5.4", "not-claude-opus-5-5"} {
		if IsClaudeModel(id) {
			t.Fatal(id)
		}
	}
	if _, ok, _ := Resolve("claude-future-9", nil); ok {
		t.Fatal("guessed future pricing")
	}
}

func TestCacheTTLAndFastCost(t *testing.T) {
	event := model.UsageEvent{Model: "claude-opus-5-5", ServiceMode: model.ServiceMode{ServiceMode: model.ModeFast}, Usage: model.TokenUsage{Input: 160, Output: 20, CachedInput: 100, CacheWriteInput: 50, CacheWrite5m: 30, CacheWrite1h: 20, Total: 180}}
	out, e := evaluateWithBasis(event, nil, FastWeightedBasis)
	if e != nil {
		t.Fatal(e)
	}
	if out.regularNano+out.cachedNano+out.cacheWriteNano+out.outputNano != 1540000 || out.pricedTokens != 180 {
		t.Fatalf("cost: %+v", out)
	}
	event.Usage.CacheWrite1h = 0
	out, e = evaluateEvent(event, nil)
	if e != nil || out.unpricedTokens != 20 || out.pricedTokens != 160 {
		t.Fatalf("unknown ttl: %+v %v", out, e)
	}
}

func TestPricingDoesNotAddThinkingTwice(t *testing.T) {
	event := model.UsageEvent{Model: "claude-sonnet-5", Usage: model.TokenUsage{Input: 10, Output: 20, ReasoningOutput: 15, Total: 30}}
	out, e := evaluateEvent(event, nil)
	if e != nil || out.regularNano+out.outputNano != 220000 {
		t.Fatalf("thinking: %+v %v", out, e)
	}
}

func TestCustomPricesRequireBothTTLs(t *testing.T) {
	in := map[string]Override{"claude-custom": {InputUSDPerMillion: "1", CachedInputUSDPerMillion: "0.1", CacheWriteInputUSDPerMillion: "1.25", OutputUSDPerMillion: "5"}}
	if _, e := NormalizeOverrides(in); e == nil {
		t.Fatal("accepted missing 1h rate")
	}
	v := in["claude-custom"]
	v.CacheWrite1hUSDPerMillion = "2"
	in["claude-custom"] = v
	if _, e := NormalizeOverrides(in); e != nil {
		t.Fatal(e)
	}
}
