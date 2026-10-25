package provider

import (
	"reflect"
	"testing"
)

func TestFamilyStem(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"gpt-4o", "gpt"},
		{"gpt-4o-mini", "gpt"},
		{"claude-opus-5", "claude-opus"},
		{"claude-sonnet-4-5", "claude-sonnet"},
		{"kimi-k3", "kimi"},
		{"kimi-k2.7-code", "kimi"},
		{"deepseek-v4-pro", "deepseek"},
		{"deepseek", "deepseek"},
		{"o3", "o"},
		{"4o", "4o"},
	} {
		if got := familyStem(tc.in); got != tc.want {
			t.Errorf("familyStem(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGenerateFallbackFamilies(t *testing.T) {
	models := []string{
		"gpt-4o", "gpt-5", "kimi-k3", "o3", "deepseek-v3",
		"claude-opus-4-5", "deepseek-v4-pro", "kimi-k2.6",
	}
	got := GenerateFallbackFamilies(models)
	// "o3" → "o" is one letter, skipped. Families newest first; the
	// deepseek/claude-opus tie (both 4) keeps first-seen order.
	want := []RouteFamilyList{
		{Name: "gpt"},
		{Name: "deepseek"},
		{Name: "claude-opus"},
		{Name: "kimi"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GenerateFallbackFamilies = %v, want %v", got, want)
	}
}
