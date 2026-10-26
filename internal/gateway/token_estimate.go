package gateway

import (
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// This file estimates a Chat relay's tokens with a tiktoken BPE codec, the
// way new-api counts them, for a vendor that answers a Chat request without
// a usage block. A vendor's own usage always wins over the estimate.

// codecs are the tiktoken encodings the estimate uses, built once each, on
// first use: each holds a ~100k-token vocabulary and a compiled regexp, so
// they are shared rather than rebuilt per request.
var (
	cl100kOnce  sync.Once
	cl100kCodec tokenizer.Codec
	o200kOnce   sync.Once
	o200kCodec  tokenizer.Codec
)

// isO200k reports whether the model is one of OpenAI's o200k_base families,
// the ones new-api picks o200k_base for. Every other model — every other
// vendor's, and every other OpenAI one — counts with cl100k_base.
func isO200k(model string) bool {
	m := strings.ToLower(model)
	for _, prefix := range []string{"gpt-4o", "gpt-4.1", "o1", "o3", "o4", "gpt-5", "chatgpt-4o"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}

// codecFor returns the model's tiktoken codec, cl100k_base by default, as
// new-api chooses it.
func codecFor(model string) tokenizer.Codec {
	if isO200k(model) {
		o200kOnce.Do(func() { o200kCodec, _ = tokenizer.Get(tokenizer.O200kBase) })
		return o200kCodec
	}
	cl100kOnce.Do(func() { cl100kCodec, _ = tokenizer.Get(tokenizer.Cl100kBase) })
	return cl100kCodec
}

// countTokens counts the model's BPE tokens in text. A text the codec won't
// take falls back to chars/4, what estimate used before there was a codec.
func countTokens(text, model string) int {
	if text == "" {
		return 0
	}
	codec := codecFor(model)
	if codec == nil {
		return len(text) / 4
	}
	n, err := codec.Count(text)
	if err != nil {
		return len(text) / 4
	}
	return n
}

// estimateTokens counts a request's tokens the way estimate counts its
// characters: the system prompt, every message part's text and tool call,
// and every tool's name, description and schema.
func estimateTokens(req *Request, model string) int {
	n := countTokens(req.System, model)
	for _, m := range req.Messages {
		for _, p := range m.Parts {
			n += countTokens(p.Text, model)
			n += countTokens(p.Name, model)
			n += countTokens(string(p.Args), model)
		}
	}
	for _, t := range req.Tools {
		n += countTokens(t.Name, model)
		n += countTokens(t.Description, model)
		n += countTokens(string(t.Schema), model)
	}
	return n
}
