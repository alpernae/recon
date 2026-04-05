package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alpernae/recon/internal/config"
)

func TestExtractIntelFindsTokensAndScopedHosts(t *testing.T) {
	t.Parallel()

	tokens, hosts := ExtractIntel("example.com", []string{
		"https://portal.example.com/auth/login",
		"https://api.example.com/v1/users",
		"https://external.test/out-of-scope",
	})

	if !contains(tokens, "auth") || !contains(tokens, "login") || !contains(tokens, "users") {
		t.Fatalf("expected extracted tokens, got %v", tokens)
	}
	if !contains(hosts, "portal.example.com") || !contains(hosts, "api.example.com") {
		t.Fatalf("expected scoped hosts, got %v", hosts)
	}
}

func TestGeneratePermutationsKeepsScopeAndDepth(t *testing.T) {
	t.Parallel()

	permutations := GeneratePermutations(
		"example.com",
		[]string{"api.example.com", "dev.example.com"},
		[]string{"portal"},
		[]string{"auth"},
		config.ModeFast,
	)

	for _, candidate := range permutations {
		if !strings.HasSuffix(candidate, ".example.com") {
			t.Fatalf("candidate escaped scope: %s", candidate)
		}
		prefix := strings.TrimSuffix(candidate, ".example.com")
		if strings.Count(prefix, ".") > 1 {
			t.Fatalf("candidate exceeded depth 2: %s", candidate)
		}
	}

	if !contains(permutations, "portal.example.com") {
		t.Fatalf("expected single-label permutation, got %v", permutations)
	}
	if contains(permutations, "a.b.c.example.com") {
		t.Fatalf("did not expect deep candidate, got %v", permutations)
	}
}

func TestNormalizeResolvedLineStripsMetadata(t *testing.T) {
	t.Parallel()

	got := normalizeResolvedLine("api.example.com [1.1.1.1]")
	if got != "api.example.com" {
		t.Fatalf("expected api.example.com, got %s", got)
	}
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func TestInScopeMatchesRootAndSubdomains(t *testing.T) {
	t.Parallel()

	if !inScope("api.example.com", "example.com") {
		t.Fatal("expected host to be in scope")
	}
	if inScope("example.org", "example.com") {
		t.Fatal("expected host to be out of scope")
	}
}

func TestPipelineOutputPathExample(t *testing.T) {
	t.Parallel()

	got := filepath.Join("workspace", "output-example.com", "resolved", "clean.txt")
	if !strings.Contains(got, "output-example.com") {
		t.Fatalf("unexpected output path %s", got)
	}
}
