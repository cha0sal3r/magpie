package agent

import (
	"strings"
	"testing"
)

// Codex gives the model its built-in image tool (image_gen.imagegen) only to
// a provider it reads as the OpenAI actor: one that does not require OpenAI
// auth but carries x-openai-actor-authorization
// (ModelProviderInfo::uses_openai_actor_authorization). So magpie-friday's own
// provider table must carry that header and must not require OpenAI auth, or
// a Codex on magpie-friday never has the tool at all and cannot draw through a Codex
// backend (sub2api's).
func TestCodexMagpieProviderCarriesActorAuthorization(t *testing.T) {
	home, read := codexHome(t, `{"OPENAI_API_KEY":"sk-relay"}`,
		"model = \"gpt-5.5\"\nmodel_provider = \"relay\"\n\n[model_providers.relay]\nname = \"relay\"\nbase_url = \"https://relay.example/v1\"\nwire_api = \"responses\"\n")
	cx := codex(home)
	if err := cx.Fields[0].Set("fake/m1"); err != nil {
		t.Fatal(err)
	}
	cfg := read()
	if !strings.Contains(cfg, "[model_providers.magpie-friday]") {
		t.Fatalf("no magpie-friday provider table:\n%s", cfg)
	}
	if !strings.Contains(cfg, `"x-openai-actor-authorization" = "magpie-friday"`) {
		t.Fatalf("magpie-friday provider table carries no actor-authorization header:\n%s", cfg)
	}
	// uses_openai_actor_authorization() also requires !requires_openai_auth:
	// the header beside that key would turn the flag off again.
	if strings.Contains(cfg, "requires_openai_auth") {
		t.Fatalf("magpie-friday provider table requires OpenAI auth, hiding the actor header:\n%s", cfg)
	}
	if c := cx.Check(); c != "" {
		t.Fatal(c)
	}
}
