package benchmark

import "strings"

// knownPrefixes maps model name prefixes to their canonical provider.
// The order matters: more specific prefixes should come first.
var knownPrefixes = []struct {
	prefix   string
	provider string
}{
	// Anthropic models
	{"claude-", "anthropic"},

	// OpenAI models
	{"gpt-", "openai"},
	{"o1-", "openai"},
	{"o3-", "openai"},
	{"o4-", "openai"},

	// Google models
	{"gemini-", "google"},

	// Mistral models
	{"mistral-", "mistral"},
	{"mixtral-", "mistral"},
}

// NormalizeModelName ensures the model name has a canonical provider prefix.
// OpenCode sometimes emits model names without the provider prefix
// (e.g. "claude-opus-4-6" instead of "anthropic/claude-opus-4-6").
// This function adds the correct prefix when it can be inferred.
// Models that already have a slash (provider/model format) are returned as-is.
// Unknown models without a slash are returned as-is.
func NormalizeModelName(model string) string {
	if model == "" {
		return ""
	}

	// Already has a provider prefix — keep it.
	if strings.Contains(model, "/") {
		return model
	}

	// Try to infer the provider from known prefixes.
	for _, kp := range knownPrefixes {
		if strings.HasPrefix(model, kp.prefix) {
			return kp.provider + "/" + model
		}
	}

	// Unknown model without prefix — return as-is.
	return model
}
