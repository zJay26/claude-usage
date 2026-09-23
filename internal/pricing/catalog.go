package pricing

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	CatalogAsOf = "2026-09-23"
	Currency    = "USD"
	Basis       = "current_standard_api_text_token_prices"
)

type Override struct {
	CacheWrite1hUSDPerMillion    string `json:"cache_write_1h_usd_per_million,omitempty"`
	AliasOf                      string `json:"alias_of,omitempty"`
	InputUSDPerMillion           string `json:"input_usd_per_million,omitempty"`
	CachedInputUSDPerMillion     string `json:"cached_input_usd_per_million,omitempty"`
	CacheWriteInputUSDPerMillion string `json:"cache_write_input_usd_per_million,omitempty"`
	OutputUSDPerMillion          string `json:"output_usd_per_million,omitempty"`
}

type CatalogEntry struct {
	CacheWrite1hUSDPerMillion    string   `json:"cache_write_1h_usd_per_million,omitempty"`
	Model                        string   `json:"model"`
	DisplayName                  string   `json:"display_name"`
	Aliases                      []string `json:"aliases,omitempty"`
	SnapshotPatterns             []string `json:"snapshot_patterns,omitempty"`
	InputUSDPerMillion           string   `json:"input_usd_per_million"`
	CachedInputUSDPerMillion     string   `json:"cached_input_usd_per_million"`
	CacheWriteInputUSDPerMillion string   `json:"cache_write_input_usd_per_million,omitempty"`
	OutputUSDPerMillion          string   `json:"output_usd_per_million"`
	Source                       string   `json:"source"`
}

type ResolvedRate struct {
	CacheWrite1hNanoPerToken *int64
	RequestedModel           string
	CanonicalModel           string
	Source                   string
	Custom                   bool
	InputNanoPerToken        int64
	CachedNanoPerToken       int64
	CacheWriteNanoPerToken   *int64
	OutputNanoPerToken       int64
}

var builtInCatalog = []CatalogEntry{
	{Model: "claude-fable-5-1", DisplayName: "Claude Fable 5.1", SnapshotPatterns: []string{"claude-fable-5-1-YYYYMMDD"}, InputUSDPerMillion: "10", CachedInputUSDPerMillion: "0.25", CacheWriteInputUSDPerMillion: "12.5", CacheWrite1hUSDPerMillion: "20", OutputUSDPerMillion: "50", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-mythos-5-1", DisplayName: "Claude Mythos 5.1", SnapshotPatterns: []string{"claude-mythos-5-1-YYYYMMDD"}, InputUSDPerMillion: "10", CachedInputUSDPerMillion: "0.25", CacheWriteInputUSDPerMillion: "12.5", CacheWrite1hUSDPerMillion: "20", OutputUSDPerMillion: "50", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-fable-5", DisplayName: "Claude Fable 5", SnapshotPatterns: []string{"claude-fable-5-YYYYMMDD"}, InputUSDPerMillion: "10", CachedInputUSDPerMillion: "1", CacheWriteInputUSDPerMillion: "12.5", CacheWrite1hUSDPerMillion: "20", OutputUSDPerMillion: "50", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-mythos-5", DisplayName: "Claude Mythos 5", SnapshotPatterns: []string{"claude-mythos-5-YYYYMMDD"}, InputUSDPerMillion: "10", CachedInputUSDPerMillion: "1", CacheWriteInputUSDPerMillion: "12.5", CacheWrite1hUSDPerMillion: "20", OutputUSDPerMillion: "50", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-5-5", DisplayName: "Claude Opus 5.5", SnapshotPatterns: []string{"claude-opus-5-5-YYYYMMDD"}, InputUSDPerMillion: "4", CachedInputUSDPerMillion: "0.2", CacheWriteInputUSDPerMillion: "5", CacheWrite1hUSDPerMillion: "8", OutputUSDPerMillion: "20", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-5", DisplayName: "Claude Opus 5", SnapshotPatterns: []string{"claude-opus-5-YYYYMMDD"}, InputUSDPerMillion: "5", CachedInputUSDPerMillion: "0.5", CacheWriteInputUSDPerMillion: "6.25", CacheWrite1hUSDPerMillion: "10", OutputUSDPerMillion: "25", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", SnapshotPatterns: []string{"claude-opus-4-8-YYYYMMDD"}, InputUSDPerMillion: "5", CachedInputUSDPerMillion: "0.5", CacheWriteInputUSDPerMillion: "6.25", CacheWrite1hUSDPerMillion: "10", OutputUSDPerMillion: "25", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-4-7", DisplayName: "Claude Opus 4.7", SnapshotPatterns: []string{"claude-opus-4-7-YYYYMMDD"}, InputUSDPerMillion: "5", CachedInputUSDPerMillion: "0.5", CacheWriteInputUSDPerMillion: "6.25", CacheWrite1hUSDPerMillion: "10", OutputUSDPerMillion: "25", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", SnapshotPatterns: []string{"claude-opus-4-6-YYYYMMDD"}, InputUSDPerMillion: "5", CachedInputUSDPerMillion: "0.5", CacheWriteInputUSDPerMillion: "6.25", CacheWrite1hUSDPerMillion: "10", OutputUSDPerMillion: "25", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-4-5", DisplayName: "Claude Opus 4.5", SnapshotPatterns: []string{"claude-opus-4-5-YYYYMMDD"}, InputUSDPerMillion: "5", CachedInputUSDPerMillion: "0.5", CacheWriteInputUSDPerMillion: "6.25", CacheWrite1hUSDPerMillion: "10", OutputUSDPerMillion: "25", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-4-1", DisplayName: "Claude Opus 4.1", SnapshotPatterns: []string{"claude-opus-4-1-YYYYMMDD"}, InputUSDPerMillion: "15", CachedInputUSDPerMillion: "1.5", CacheWriteInputUSDPerMillion: "18.75", CacheWrite1hUSDPerMillion: "30", OutputUSDPerMillion: "75", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-opus-4", DisplayName: "Claude Opus 4", SnapshotPatterns: []string{"claude-opus-4-YYYYMMDD"}, InputUSDPerMillion: "15", CachedInputUSDPerMillion: "1.5", CacheWriteInputUSDPerMillion: "18.75", CacheWrite1hUSDPerMillion: "30", OutputUSDPerMillion: "75", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", SnapshotPatterns: []string{"claude-sonnet-5-YYYYMMDD"}, InputUSDPerMillion: "2", CachedInputUSDPerMillion: "0.2", CacheWriteInputUSDPerMillion: "2.5", CacheWrite1hUSDPerMillion: "4", OutputUSDPerMillion: "10", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", SnapshotPatterns: []string{"claude-sonnet-4-6-YYYYMMDD"}, InputUSDPerMillion: "3", CachedInputUSDPerMillion: "0.3", CacheWriteInputUSDPerMillion: "3.75", CacheWrite1hUSDPerMillion: "6", OutputUSDPerMillion: "15", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", SnapshotPatterns: []string{"claude-sonnet-4-5-YYYYMMDD"}, InputUSDPerMillion: "3", CachedInputUSDPerMillion: "0.3", CacheWriteInputUSDPerMillion: "3.75", CacheWrite1hUSDPerMillion: "6", OutputUSDPerMillion: "15", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-sonnet-4", DisplayName: "Claude Sonnet 4", SnapshotPatterns: []string{"claude-sonnet-4-YYYYMMDD"}, InputUSDPerMillion: "3", CachedInputUSDPerMillion: "0.3", CacheWriteInputUSDPerMillion: "3.75", CacheWrite1hUSDPerMillion: "6", OutputUSDPerMillion: "15", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5", SnapshotPatterns: []string{"claude-haiku-4-5-YYYYMMDD"}, InputUSDPerMillion: "1", CachedInputUSDPerMillion: "0.1", CacheWriteInputUSDPerMillion: "1.25", CacheWrite1hUSDPerMillion: "2", OutputUSDPerMillion: "5", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
	{Model: "claude-3-5-haiku", DisplayName: "Claude Haiku 3.5", SnapshotPatterns: []string{"claude-3-5-haiku-YYYYMMDD"}, InputUSDPerMillion: "0.8", CachedInputUSDPerMillion: "0.08", CacheWriteInputUSDPerMillion: "1", CacheWrite1hUSDPerMillion: "1.6", OutputUSDPerMillion: "4", Source: "https://platform.claude.com/docs/en/about-claude/pricing"},
}

func Catalog() []CatalogEntry {
	out := make([]CatalogEntry, len(builtInCatalog))
	copy(out, builtInCatalog)
	for i := range out {
		out[i].Aliases = append([]string(nil), out[i].Aliases...)
		out[i].SnapshotPatterns = append([]string(nil), out[i].SnapshotPatterns...)
	}
	return out
}

func NormalizeOverrides(input map[string]Override) (map[string]Override, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make(map[string]Override, len(input))
	for rawModel, raw := range input {
		model := strings.ToLower(strings.TrimSpace(rawModel))
		if model == "" || len(model) > 200 || strings.ContainsAny(model, "\x00\r\n") {
			return nil, fmt.Errorf("无效定价覆写模型 %q", rawModel)
		}
		if _, exists := out[model]; exists {
			return nil, fmt.Errorf("重复定价覆写模型 %q", model)
		}
		raw.AliasOf = strings.ToLower(strings.TrimSpace(raw.AliasOf))
		raw.InputUSDPerMillion = strings.TrimSpace(raw.InputUSDPerMillion)
		raw.CachedInputUSDPerMillion = strings.TrimSpace(raw.CachedInputUSDPerMillion)
		raw.CacheWriteInputUSDPerMillion = strings.TrimSpace(raw.CacheWriteInputUSDPerMillion)
		raw.OutputUSDPerMillion = strings.TrimSpace(raw.OutputUSDPerMillion)
		raw.CacheWrite1hUSDPerMillion = strings.TrimSpace(raw.CacheWrite1hUSDPerMillion)
		if _, ok := resolveBuiltIn(model); ok {
			return nil, fmt.Errorf("内置官方模型 %q 不能被本机覆写", model)
		}
		if raw.AliasOf != "" {
			if raw.InputUSDPerMillion != "" || raw.CachedInputUSDPerMillion != "" ||
				raw.CacheWriteInputUSDPerMillion != "" || raw.CacheWrite1hUSDPerMillion != "" || raw.OutputUSDPerMillion != "" {
				return nil, fmt.Errorf("模型 %q 的 alias_of 与自定义单价不能同时设置", model)
			}
			entry, ok := resolveBuiltIn(raw.AliasOf)
			if !ok {
				return nil, fmt.Errorf("模型 %q 的 alias_of 必须指向内置公开模型", model)
			}
			raw.AliasOf = entry.Model
		} else {
			if raw.InputUSDPerMillion == "" || raw.CachedInputUSDPerMillion == "" ||
				raw.CacheWriteInputUSDPerMillion == "" || raw.CacheWrite1hUSDPerMillion == "" || raw.OutputUSDPerMillion == "" {
				return nil, fmt.Errorf("模型 %q 必须填写 input、cached input、cache write 和 output 单价", model)
			}
			for label, value := range map[string]string{
				"input": raw.InputUSDPerMillion, "cached input": raw.CachedInputUSDPerMillion,
				"cache write 1h": raw.CacheWrite1hUSDPerMillion, "cache write": raw.CacheWriteInputUSDPerMillion, "output": raw.OutputUSDPerMillion,
			} {
				if _, err := parseUSDPerMillion(value); err != nil {
					return nil, fmt.Errorf("模型 %q 的 %s 单价: %w", model, label, err)
				}
			}
		}
		out[model] = raw
	}
	return out, nil
}

func Resolve(model string, overrides map[string]Override) (ResolvedRate, bool, error) {
	requested := NormalizeModel(model)
	if override, ok := overrides[requested]; ok {
		if override.AliasOf != "" {
			entry, found := resolveBuiltIn(override.AliasOf)
			if !found {
				return ResolvedRate{}, false, fmt.Errorf("alias_of %q 不再存在", override.AliasOf)
			}
			rate, err := resolvedFromEntry(requested, entry)
			rate.Custom = true
			return rate, true, err
		}
		rate, err := resolvedFromOverride(requested, override)
		return rate, true, err
	}
	entry, ok := resolveBuiltIn(requested)
	if !ok {
		return ResolvedRate{}, false, nil
	}
	rate, err := resolvedFromEntry(requested, entry)
	return rate, true, err
}

func resolveBuiltIn(model string) (CatalogEntry, bool) {
	model = NormalizeModel(model)
	for _, entry := range builtInCatalog {
		if modelMatches(model, entry.Model) {
			return entry, true
		}
		for _, alias := range entry.Aliases {
			if modelMatches(model, alias) {
				return entry, true
			}
		}
	}
	return CatalogEntry{}, false
}

func modelMatches(model, base string) bool {
	if model == base {
		return true
	}
	if !strings.HasPrefix(model, base+"-") {
		return false
	}
	suffix := strings.TrimPrefix(model, base+"-")
	if len(suffix) == 8 {
		_, err := time.Parse("20060102", suffix)
		return err == nil
	}
	if len(suffix) != len("2006-01-02") || suffix[4] != '-' || suffix[7] != '-' {
		return false
	}
	_, err := time.Parse("2006-01-02", suffix)
	return err == nil
}

func resolvedFromEntry(requested string, entry CatalogEntry) (ResolvedRate, error) {
	input, err := parseUSDPerMillion(entry.InputUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	cached, err := parseUSDPerMillion(entry.CachedInputUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	output, err := parseUSDPerMillion(entry.OutputUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	var cacheWrite *int64
	if entry.CacheWriteInputUSDPerMillion != "" {
		value, parseErr := parseUSDPerMillion(entry.CacheWriteInputUSDPerMillion)
		if parseErr != nil {
			return ResolvedRate{}, parseErr
		}
		cacheWrite = &value
	}
	hour, err := optionalRate(entry.CacheWrite1hUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	rate := ResolvedRate{
		CacheWrite1hNanoPerToken: hour,
		RequestedModel:           requested, CanonicalModel: entry.Model, Source: entry.Source,
		InputNanoPerToken: input, CachedNanoPerToken: cached,
		CacheWriteNanoPerToken: cacheWrite, OutputNanoPerToken: output,
	}
	return rate, nil
}

func resolvedFromOverride(model string, override Override) (ResolvedRate, error) {
	input, err := parseUSDPerMillion(override.InputUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	cached, err := parseUSDPerMillion(override.CachedInputUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	output, err := parseUSDPerMillion(override.OutputUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	var cacheWrite *int64
	if override.CacheWriteInputUSDPerMillion != "" {
		value, parseErr := parseUSDPerMillion(override.CacheWriteInputUSDPerMillion)
		if parseErr != nil {
			return ResolvedRate{}, parseErr
		}
		cacheWrite = &value
	}
	hour, err := optionalRate(override.CacheWrite1hUSDPerMillion)
	if err != nil {
		return ResolvedRate{}, err
	}
	return ResolvedRate{
		CacheWrite1hNanoPerToken: hour,
		RequestedModel:           model, CanonicalModel: model, Source: "local_override", Custom: true,
		InputNanoPerToken: input, CachedNanoPerToken: cached,
		CacheWriteNanoPerToken: cacheWrite, OutputNanoPerToken: output,
	}, nil
}

// parseUSDPerMillion converts a USD/1M-token decimal with at most three
// fractional digits to nano-USD/token. For example, 0.075 becomes 75.
func parseUSDPerMillion(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "eE") {
		return 0, fmt.Errorf("必须是非负十进制字符串")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("必须是非负十进制字符串")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 1_000_000 {
		return 0, fmt.Errorf("数值超出范围")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > 3 {
			return 0, fmt.Errorf("最多保留三位小数")
		}
		for _, char := range fraction {
			if char < '0' || char > '9' {
				return 0, fmt.Errorf("必须是非负十进制字符串")
			}
		}
	}
	fraction += strings.Repeat("0", 3-len(fraction))
	frac := int64(0)
	if fraction != "" {
		frac, _ = strconv.ParseInt(fraction, 10, 64)
	}
	return whole*1000 + frac, nil
}

func optionalRate(value string) (*int64, error) {
	if value == "" {
		return nil, nil
	}
	n, e := parseUSDPerMillion(value)
	return &n, e
}

var providerVersion = regexp.MustCompile("-v[0-9]+(:[0-9]+)?$")
var claudeName = regexp.MustCompile("^claude-[a-z0-9][a-z0-9.-]*$")

func NormalizeModel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"us.anthropic.", "eu.anthropic.", "apac.anthropic.", "global.anthropic.", "anthropic."} {
		value = strings.TrimPrefix(value, prefix)
	}
	value = strings.ReplaceAll(value, "@", "-")
	return providerVersion.ReplaceAllString(value, "")
}
func IsClaudeModel(value string) bool { return claudeName.MatchString(NormalizeModel(value)) }
