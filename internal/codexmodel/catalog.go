package codexmodel

import (
	"context"
	"strings"
	"unicode/utf8"

	codexauth "github.com/mattsp1290/codex-auth-go"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

const (
	ReasoningEffortLow    = "low"
	ReasoningEffortMedium = "medium"
	ReasoningEffortHigh   = "high"

	MaxCatalogEntries           = 256
	MaxCatalogPresentationBytes = 1 << 20
	MaxModelDisplayNameBytes    = 256
	MaxModelDescriptionBytes    = 2048
	MaxEffortDescriptionBytes   = 512
)

// ReasoningEffort is one provider-supported effort and its safe presentation label.
type ReasoningEffort struct {
	ID          string
	Description string
}

// CatalogEntry is account catalog metadata safe to retain in application state.
type CatalogEntry struct {
	ModelID          agentmodel.ID
	DisplayName      string
	Description      string
	Priority         int
	DefaultEffort    string
	SupportedEfforts []ReasoningEffort
}

// Catalog retrieves a fresh normalized account model catalog.
type Catalog interface {
	ListModels(context.Context) ([]CatalogEntry, error)
}

// ValidateCatalogModelID enforces the durable provider-state identity contract.
func ValidateCatalogModelID(value string) error {
	if agentmodel.ValidateProviderStateIdentity(string(ProviderID), value) != nil {
		return ErrInvalidModel
	}
	return nil
}

// ValidReasoningEffort reports whether effort is part of the provider's public contract.
func ValidReasoningEffort(effort string) bool {
	switch effort {
	case ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh:
		return true
	default:
		return false
	}
}

// NormalizeCatalog admits, sanitizes, and resource-bounds picker-visible entries.
func NormalizeCatalog(source []codexauth.ModelCatalogEntry) []CatalogEntry {
	result := make([]CatalogEntry, 0, min(len(source), MaxCatalogEntries))
	seen := make(map[string]struct{}, len(source))
	total := 0
	for _, remote := range source {
		if len(result) == MaxCatalogEntries {
			break
		}
		if _, exists := seen[remote.Slug]; exists {
			continue
		}
		seen[remote.Slug] = struct{}{}
		if ValidateCatalogModelID(remote.Slug) != nil {
			continue
		}
		efforts := normalizeEfforts(remote.SupportedReasoningEfforts)
		if len(efforts) == 0 {
			continue
		}
		name := normalizeField(remote.DisplayName, MaxModelDisplayNameBytes)
		if name == "" {
			name = remote.Slug
		}
		description := ""
		if remote.Description != nil {
			description = normalizeField(*remote.Description, MaxModelDescriptionBytes)
		}
		entry := CatalogEntry{
			ModelID:          agentmodel.ID(remote.Slug),
			DisplayName:      name,
			Description:      description,
			Priority:         remote.Priority,
			DefaultEffort:    effectiveDefault(remote.DefaultReasoningEffort, efforts),
			SupportedEfforts: efforts,
		}
		size := len(remote.Slug) + len(name) + len(description)
		for _, effort := range efforts {
			size += len(effort.ID) + len(effort.Description)
		}
		if size > MaxCatalogPresentationBytes-total {
			break
		}
		total += size
		result = append(result, entry)
	}
	return result
}

func normalizeEfforts(source []codexauth.ReasoningEffortOption) []ReasoningEffort {
	result := make([]ReasoningEffort, 0, 3)
	seen := make(map[string]struct{}, 3)
	for _, remote := range source {
		if !ValidReasoningEffort(remote.Effort) {
			continue
		}
		if _, exists := seen[remote.Effort]; exists {
			continue
		}
		seen[remote.Effort] = struct{}{}
		result = append(result, ReasoningEffort{ID: remote.Effort, Description: normalizeField(remote.Description, MaxEffortDescriptionBytes)})
	}
	return result
}

func effectiveDefault(remote *string, efforts []ReasoningEffort) string {
	if remote != nil {
		for _, effort := range efforts {
			if effort.ID == *remote {
				return effort.ID
			}
		}
	}
	for _, effort := range efforts {
		if effort.ID == ReasoningEffortMedium {
			return effort.ID
		}
	}
	return efforts[0].ID
}

func normalizeField(value string, limit int) string {
	value = strings.Join(strings.Fields(textsafe.Display(value)), " ")
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}
