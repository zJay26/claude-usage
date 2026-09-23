package pricing

import (
	"fmt"
	"time"

	"github.com/zJay26/claude-usage/internal/model"
)

const FastWeightedBasis = "claude_fast_weighted"
const FastRulesAsOf = "2026-09-23"
const FastRulesSource = "https://platform.claude.com/docs/en/about-claude/pricing#fast-mode-pricing"

func ValidBasis(basis string) bool {
	return basis == "" || basis == Basis || basis == FastWeightedBasis
}

func NewBuilderForBasis(overrides map[string]Override, basis string, locations ...*time.Location) (*Builder, error) {
	if !ValidBasis(basis) {
		return nil, fmt.Errorf("不支持的计价口径 %q", basis)
	}
	b, err := NewBuilder(overrides)
	if err != nil {
		return nil, err
	}
	if len(locations) > 0 {
		b.location = locations[0]
	}
	if basis != "" {
		b.basis = basis
	}
	return b, nil
}

// Unknown models and custom rates never inherit a Fast price by name prefix.
func FastMultiplier(canonical string) (int64, int64, bool) {
	// Only catalog aliases and dated snapshots can resolve to this allowlist.
	if rate, found, err := Resolve(canonical, nil); err == nil && found {
		canonical = rate.CanonicalModel
	}
	switch canonical {
	case "claude-opus-5-5", "claude-opus-5", "claude-opus-4-8":
		return 2, 1, true
	case "claude-opus-4-6":
		return 1, 1, true // The current API ignores Fast for this model.
	default:
		return 0, 0, false
	}
}

func evaluateWithBasis(event model.UsageEvent, overrides map[string]Override, basis string) (evaluatedEvent, error) {
	out, err := evaluateEvent(event, overrides)
	if err != nil {
		return out, err
	}
	out.mode = event.ServiceMode.Normalized().ServiceMode
	for _, n := range []int64{out.regularNano, out.cachedNano, out.cacheWriteNano, out.outputNano} {
		out.baseNano, err = checkedAdd(out.baseNano, n)
		if err != nil {
			return out, err
		}
	}
	if basis != FastWeightedBasis || out.mode != model.ModeFast || out.pricedTokens == 0 {
		return out, nil
	}
	rate, found, err := Resolve(event.Model, overrides)
	if err != nil {
		return out, err
	}
	n, d, ok := FastMultiplier(rate.CanonicalModel)
	if !found || !ok {
		out.reasons = append(out.reasons, UnpricedReason{Kind: "fast_multiplier_missing", Model: event.Model, Tokens: out.pricedTokens, Detail: "没有已确认的 Claude Fast API 单价"})
		out.unpricedTokens += out.pricedTokens
		out.pricedTokens = 0
		out.regularNano, out.cachedNano, out.cacheWriteNano, out.outputNano = 0, 0, 0, 0
		return out, nil
	}
	for _, cost := range []*int64{&out.regularNano, &out.cachedNano, &out.cacheWriteNano, &out.outputNano} {
		before := *cost
		*cost, err = tokenCost(before, 1, n, d)
		if err != nil {
			return out, err
		}
		out.surchargeNano, err = checkedAdd(out.surchargeNano, *cost-before)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
