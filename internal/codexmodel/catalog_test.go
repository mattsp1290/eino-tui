package codexmodel

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	codexauth "github.com/mattsp1290/codex-auth-go"
)

func catalogString(value string) *string { return &value }

func remoteCatalogEntry(slug string, efforts ...string) codexauth.ModelCatalogEntry {
	options := make([]codexauth.ReasoningEffortOption, len(efforts))
	for i, effort := range efforts {
		options[i] = codexauth.ReasoningEffortOption{Effort: effort, Description: effort + " description"}
	}
	return codexauth.ModelCatalogEntry{
		Slug: slug, DisplayName: slug + " display", Description: catalogString("model description"),
		DefaultReasoningEffort: catalogString(ReasoningEffortMedium), SupportedReasoningEfforts: options,
	}
}

func TestNormalizeCatalogPreservesAuthoritativeOrderAndClosedEfforts(t *testing.T) {
	first := remoteCatalogEntry("o4-mini-live", "xhigh", "low", "low", "high")
	first.DisplayName = "  Safe\tname\nnext\u2028line \x1b]0;CANARY\a"
	first.Description = nil
	first.DefaultReasoningEffort = catalogString("xhigh")
	duplicate := remoteCatalogEntry("o4-mini-live", "medium")
	second := remoteCatalogEntry("gpt-5.6", "unknown", "medium", "high")
	second.DefaultReasoningEffort = catalogString("unknown")
	invalid := remoteCatalogEntry("bad model", "medium")
	empty := remoteCatalogEntry("empty-efforts", "xhigh")

	got := NormalizeCatalog([]codexauth.ModelCatalogEntry{first, duplicate, invalid, empty, second})
	if len(got) != 2 || got[0].ModelID != "o4-mini-live" || got[1].ModelID != "gpt-5.6" {
		t.Fatalf("catalog order/filter = %#v", got)
	}
	if ValidateModel(string(got[0].ModelID)) == nil {
		t.Fatal("live non-gpt slug unexpectedly passed startup policy")
	}
	if got[0].DisplayName != "Safe name next line" || got[0].Description != "" || strings.Contains(fmt.Sprint(got), "CANARY") {
		t.Fatalf("unsafe normalized presentation = %#v", got[0])
	}
	if got[0].DefaultEffort != "low" || len(got[0].SupportedEfforts) != 2 || got[0].SupportedEfforts[0].ID != "low" || got[0].SupportedEfforts[1].ID != "high" {
		t.Fatalf("first efforts = %#v", got[0])
	}
	if got[1].DefaultEffort != "medium" {
		t.Fatalf("medium fallback = %#v", got[1])
	}
}

func TestNormalizeCatalogFallbacksAndUTF8SafeFieldBounds(t *testing.T) {
	entry := remoteCatalogEntry("gpt-5.6", "high")
	entry.DisplayName = " \t\n "
	entry.Description = catalogString(strings.Repeat("界", MaxModelDescriptionBytes))
	entry.SupportedReasoningEfforts[0].Description = strings.Repeat("λ", MaxEffortDescriptionBytes)
	got := NormalizeCatalog([]codexauth.ModelCatalogEntry{entry})
	if len(got) != 1 || got[0].DisplayName != entry.Slug || got[0].DefaultEffort != "high" {
		t.Fatalf("fallback = %#v", got)
	}
	if len(got[0].Description) > MaxModelDescriptionBytes || !utf8.ValidString(got[0].Description) ||
		len(got[0].SupportedEfforts[0].Description) > MaxEffortDescriptionBytes || !utf8.ValidString(got[0].SupportedEfforts[0].Description) {
		t.Fatalf("invalid UTF-8 truncation = %#v", got[0])
	}
}

func TestNormalizeCatalogResourceBoundsRetainDeterministicPrefix(t *testing.T) {
	entries := make([]codexauth.ModelCatalogEntry, MaxCatalogEntries+1)
	for i := range entries {
		entries[i] = remoteCatalogEntry(fmt.Sprintf("catalog-model-%03d", i), "low", "medium", "high")
		entries[i].Description = catalogString(strings.Repeat("d", MaxModelDescriptionBytes))
		for j := range entries[i].SupportedReasoningEfforts {
			entries[i].SupportedReasoningEfforts[j].Description = strings.Repeat("e", MaxEffortDescriptionBytes)
		}
	}
	got := NormalizeCatalog(entries)
	if len(got) == 0 || len(got) > MaxCatalogEntries || got[0].ModelID != "catalog-model-000" {
		t.Fatalf("bounded prefix length=%d first=%#v", len(got), got[0])
	}
	if len(got) == len(entries) || string(got[len(got)-1].ModelID) != agentCatalogID(len(got)-1) {
		t.Fatalf("non-prefix result length=%d last=%q", len(got), got[len(got)-1].ModelID)
	}
	total := 0
	for _, entry := range got {
		total += len(entry.ModelID) + len(entry.DisplayName) + len(entry.Description)
		for _, effort := range entry.SupportedEfforts {
			total += len(effort.ID) + len(effort.Description)
		}
	}
	if total > MaxCatalogPresentationBytes {
		t.Fatalf("catalog bytes = %d", total)
	}
}

func agentCatalogID(index int) string { return fmt.Sprintf("catalog-model-%03d", index) }
