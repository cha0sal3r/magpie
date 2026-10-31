package cli

import (
	"strings"
	"testing"

	"github.com/cha0sal3r/magpie-friday/internal/provider"
)

func TestShowProviderMasksSecretHeaders(t *testing.T) {
	p := provider.Provider{
		ID:   "test",
		Name: "Test",
		Chat: "http://127.0.0.1:8080/v1",
		Headers: map[string]string{
			"Authorization": "private-test-token",
			"X-Client-Name": "magpie-friday-test",
		},
	}
	out, err := said(t, func() error { return showProvider(p) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "private-test-token") || !strings.Contains(out, provider.Mask("private-test-token")) {
		t.Fatalf("secret header was not masked:\n%s", out)
	}
	if !strings.Contains(out, "X-Client-Name: magpie-friday-test") {
		t.Fatalf("ordinary header was unexpectedly masked:\n%s", out)
	}
}
