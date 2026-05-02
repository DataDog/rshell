// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package autoresearch

import (
	"fmt"
	"strings"
)

const (
	FeedbackTagScopedAccess          = "scoped_access"
	FeedbackTagBoundedInspection     = "bounded_inspection"
	FeedbackTagCommandDiscovery      = "command_discovery"
	FeedbackTagDiagnosticCorrelation = "diagnostic_correlation"
	FeedbackTagEvidenceGrounding     = "evidence_grounding"
	FeedbackTagSafeNextSteps         = "safe_next_steps"
	FeedbackTagUncertaintyHandling   = "uncertainty_handling"
	FeedbackTagConcision             = "concision"
)

var feedbackTagDescriptions = map[string]string{
	FeedbackTagScopedAccess:          "Constrain rshell filesystem access to prompt-provided roots and inspect files only through ./rshell.",
	FeedbackTagBoundedInspection:     "Prefer narrow, bounded, read-only probes over broad dumps, repeated searches, or unbounded log reads.",
	FeedbackTagCommandDiscovery:      "Check rshell and builtin help before assuming flags, and adapt when a familiar system-tool flag is unavailable.",
	FeedbackTagDiagnosticCorrelation: "Correlate symptom evidence across relevant logs or layers, and separate the likely cause from unrelated noise.",
	FeedbackTagEvidenceGrounding:     "Ground conclusions in explicit command, file, and output observations rather than unsupported assertions.",
	FeedbackTagSafeNextSteps:         "Keep recommendations to safe read-only follow-up checks; leave remediation actions to an operator.",
	FeedbackTagUncertaintyHandling:   "State confidence and missing evidence when the available output is incomplete or ambiguous.",
	FeedbackTagConcision:             "Make one concise, general improvement instead of adding long or special-case guidance.",
}

var feedbackTagOrder = []string{
	FeedbackTagScopedAccess,
	FeedbackTagBoundedInspection,
	FeedbackTagCommandDiscovery,
	FeedbackTagDiagnosticCorrelation,
	FeedbackTagEvidenceGrounding,
	FeedbackTagSafeNextSteps,
	FeedbackTagUncertaintyHandling,
	FeedbackTagConcision,
}

// FeedbackTagOrder returns the stable disclosure order for sanitized feedback tags.
func FeedbackTagOrder() []string {
	return append([]string(nil), feedbackTagOrder...)
}

// FeedbackTagDescription returns the generic, benchmark-agnostic text for a tag.
func FeedbackTagDescription(tag string) (string, bool) {
	description, ok := feedbackTagDescriptions[strings.TrimSpace(tag)]
	return description, ok
}

// NormalizeFeedbackTags trims, deduplicates, and canonicalizes known feedback tags
// into the stable disclosure order. Unknown tags are omitted; callers that load
// trusted benchmark definitions should use ValidateFeedbackTags to reject them.
func NormalizeFeedbackTags(tags []string) []string {
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if _, ok := feedbackTagDescriptions[tag]; ok {
			seen[tag] = true
		}
	}
	normalized := make([]string, 0, len(seen))
	for _, tag := range feedbackTagOrder {
		if seen[tag] {
			normalized = append(normalized, tag)
		}
	}
	return normalized
}

// ValidateFeedbackTags rejects tags outside the closed sanitized feedback taxonomy.
func ValidateFeedbackTags(tags []string) error {
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return fmt.Errorf("empty feedback tag")
		}
		if _, ok := feedbackTagDescriptions[tag]; !ok {
			return fmt.Errorf("unknown feedback tag %q", tag)
		}
	}
	return nil
}
