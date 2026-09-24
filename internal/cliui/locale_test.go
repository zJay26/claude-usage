package cliui

import (
	"reflect"
	"sort"
	"testing"
)

func TestCatalogsHaveIdenticalKeys(t *testing.T) {
	keys := func(locale Locale) []string {
		result := make([]string, 0, len(messages[locale]))
		for key := range messages[locale] {
			result = append(result, key)
		}
		sort.Strings(result)
		return result
	}
	if !reflect.DeepEqual(keys(Chinese), keys(English)) {
		t.Fatalf("Chinese and English CLI catalogs must have identical keys")
	}
}

func TestDetectLocalePrecedence(t *testing.T) {
	tests := []struct {
		name, explicit, environment string
		want                        Locale
	}{
		{"flag", "zh-CN", "en", Chinese},
		{"environment", "", "en", English},
		{"default", "", "", Chinese},
		{"invalid environment", "", "fr-FR", Chinese},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Detect(test.explicit, test.environment)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}

func TestInvalidExplicitLocale(t *testing.T) {
	if _, err := Detect("fr", "en"); err == nil {
		t.Fatal("expected invalid explicit locale to fail")
	}
}
