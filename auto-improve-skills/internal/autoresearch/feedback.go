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

	FeedbackTagScopedAccessUsePromptRoots        = "scoped_access.use_prompt_roots"
	FeedbackTagScopedAccessNoDirectArtifactReads = "scoped_access.no_direct_artifact_reads"
	FeedbackTagScopedAccessNoRemoteActionClaims  = "scoped_access.no_remote_action_claims"
	FeedbackTagScopedAccessHandlePromptRoots     = "scoped_access.handle_prompt_roots"

	FeedbackTagBoundedInspectionNarrowFilters               = "bounded_inspection.narrow_filters"
	FeedbackTagBoundedInspectionLimitLogReads               = "bounded_inspection.limit_log_reads"
	FeedbackTagBoundedInspectionStopAfterSufficientEvidence = "bounded_inspection.stop_after_sufficient_evidence"
	FeedbackTagBoundedInspectionAvoidRepeatedBroadSearches  = "bounded_inspection.avoid_repeated_broad_searches"

	FeedbackTagCommandDiscoveryCheckBuiltinHelp          = "command_discovery.check_builtin_help"
	FeedbackTagCommandDiscoveryVerifySupportedFlags      = "command_discovery.verify_supported_flags"
	FeedbackTagCommandDiscoveryAdaptAfterUnsupportedFlag = "command_discovery.adapt_after_unsupported_flag"

	FeedbackTagDiagnosticCorrelationConnectSymptomToCause      = "diagnostic_correlation.connect_symptom_to_cause"
	FeedbackTagDiagnosticCorrelationCompareLogsAcrossLayers    = "diagnostic_correlation.compare_logs_across_layers"
	FeedbackTagDiagnosticCorrelationDistinguishSignalFromNoise = "diagnostic_correlation.distinguish_signal_from_noise"
	FeedbackTagDiagnosticCorrelationQuantifyPatterns           = "diagnostic_correlation.quantify_patterns"

	FeedbackTagEvidenceGroundingCiteKeyOutputs               = "evidence_grounding.cite_key_outputs"
	FeedbackTagEvidenceGroundingCiteSourceFiles              = "evidence_grounding.cite_source_files"
	FeedbackTagEvidenceGroundingSeparateObservedFromInferred = "evidence_grounding.separate_observed_from_inferred"
	FeedbackTagEvidenceGroundingSupportCountsWithCommands    = "evidence_grounding.support_counts_with_commands"

	FeedbackTagSafeNextStepsReadOnlyFollowups        = "safe_next_steps.read_only_followups"
	FeedbackTagSafeNextStepsAvoidRemediationCommands = "safe_next_steps.avoid_remediation_commands"

	FeedbackTagUncertaintyHandlingStateMissingEvidence = "uncertainty_handling.state_missing_evidence"
	FeedbackTagUncertaintyHandlingAvoidOverclaiming    = "uncertainty_handling.avoid_overclaiming"
	FeedbackTagUncertaintyHandlingExplainLimitations   = "uncertainty_handling.explain_limitations"

	FeedbackTagConcisionOneSmallGeneralChange = "concision.one_small_general_change"
	FeedbackTagConcisionAvoidSpecialCaseRules = "concision.avoid_special_case_rules"
)

type feedbackCard struct {
	ID          string
	ParentID    string
	Title       string
	Description string
}

var feedbackCards = []feedbackCard{
	{
		ID:          FeedbackTagScopedAccess,
		Title:       "Constrain filesystem scope",
		Description: "Constrain rshell filesystem access to prompt-provided roots and inspect files only through ./rshell.",
	},
	{
		ID:          FeedbackTagScopedAccessUsePromptRoots,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Use prompt-provided roots",
		Description: "Set rshell access to only the roots supplied in the prompt, and avoid searching outside that declared scope.",
	},
	{
		ID:          FeedbackTagScopedAccessNoDirectArtifactReads,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Inspect evidence through rshell",
		Description: "Inspect diagnostic data through ./rshell rather than direct workspace file reads or evaluator artifacts.",
	},
	{
		ID:          FeedbackTagScopedAccessNoRemoteActionClaims,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Avoid unsupported remote-access claims",
		Description: "Describe only the local rshell outputs that were observed; do not imply a separate remote-action tool or real host contact unless the transcript shows it.",
	},
	{
		ID:          FeedbackTagScopedAccessHandlePromptRoots,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Handle multiple prompt roots explicitly",
		Description: "When the prompt gives primary and alternate roots, check each named root deliberately and explain which one contained useful evidence.",
	},

	{
		ID:          FeedbackTagBoundedInspection,
		Title:       "Keep inspection bounded",
		Description: "Prefer narrow, bounded, read-only probes over broad dumps, repeated searches, or unbounded log reads.",
	},
	{
		ID:          FeedbackTagBoundedInspectionNarrowFilters,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Use narrow read-only filters",
		Description: "Use targeted grep/find/head/tail/wc-style probes around likely signals before reading large files or broad directory trees.",
	},
	{
		ID:          FeedbackTagBoundedInspectionLimitLogReads,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Limit log volume",
		Description: "Bound log reads with line limits, filters, or recent windows so the investigation gathers signal without dumping whole logs.",
	},
	{
		ID:          FeedbackTagBoundedInspectionStopAfterSufficientEvidence,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Stop after enough evidence",
		Description: "Once the likely cause is supported by the key observations, stop collecting more data and move to a concise answer.",
	},
	{
		ID:          FeedbackTagBoundedInspectionAvoidRepeatedBroadSearches,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Avoid repeated broad searches",
		Description: "Do not repeat broad searches with small wording variations when a narrower query or final synthesis would answer the prompt.",
	},

	{
		ID:          FeedbackTagCommandDiscovery,
		Title:       "Discover command support",
		Description: "Check rshell and builtin help before assuming flags, and adapt when a familiar system-tool flag is unavailable.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryCheckBuiltinHelp,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Check builtin help first",
		Description: "Run rshell help or builtin-specific help early when command availability or supported flags matter.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryVerifySupportedFlags,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Verify supported flags",
		Description: "Prefer documented rshell-supported flags over familiar full-system variants, especially for compatibility-sensitive builtins.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryAdaptAfterUnsupportedFlag,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Recover from unsupported flags",
		Description: "If a flag is unsupported, switch to a supported narrower command and state any resulting information limits.",
	},

	{
		ID:          FeedbackTagDiagnosticCorrelation,
		Title:       "Correlate diagnostics",
		Description: "Correlate symptom evidence across relevant logs or layers, and separate the likely cause from unrelated noise.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationConnectSymptomToCause,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Connect symptoms to cause",
		Description: "Tie the user-visible symptom to the likely lower-level cause with observations from the relevant logs or command outputs.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationCompareLogsAcrossLayers,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Compare relevant layers",
		Description: "Check the small set of relevant application, proxy, system, or service logs needed to confirm cross-layer consistency.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationDistinguishSignalFromNoise,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Separate signal from noise",
		Description: "Call out unrelated or secondary errors as noise only after contrasting them with evidence for the likely cause.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationQuantifyPatterns,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Quantify recurring patterns",
		Description: "When the prompt asks for scale or frequency, use bounded counts or grouping to summarize the pattern instead of relying on examples alone.",
	},

	{
		ID:          FeedbackTagEvidenceGrounding,
		Title:       "Ground conclusions in evidence",
		Description: "Ground conclusions in explicit command, file, and output observations rather than unsupported assertions.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingCiteKeyOutputs,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Cite key output",
		Description: "For each major conclusion, cite the command output or log line pattern that directly supports it.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingCiteSourceFiles,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Name evidence sources",
		Description: "Name the relevant files or command sources in the final answer so the diagnosis is auditable.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingSeparateObservedFromInferred,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Separate observed from inferred",
		Description: "Distinguish directly observed facts from likely interpretation, especially when stating a root cause or absence of evidence.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingSupportCountsWithCommands,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Support counts with commands",
		Description: "When giving counts or approximate scale, back them with a bounded count/grouping command or an explicit caveat.",
	},

	{
		ID:          FeedbackTagSafeNextSteps,
		Title:       "Keep next steps safe",
		Description: "Keep recommendations to safe read-only follow-up checks; leave remediation actions to an operator.",
	},
	{
		ID:          FeedbackTagSafeNextStepsReadOnlyFollowups,
		ParentID:    FeedbackTagSafeNextSteps,
		Title:       "Recommend read-only follow-ups",
		Description: "Suggest only diagnostic follow-up checks that inspect state or metrics without changing services, files, or configuration.",
	},
	{
		ID:          FeedbackTagSafeNextStepsAvoidRemediationCommands,
		ParentID:    FeedbackTagSafeNextSteps,
		Title:       "Avoid remediation commands",
		Description: "Do not recommend restart, kill, delete, edit, or apply-style actions as the agent's next step; leave remediation to an operator.",
	},

	{
		ID:          FeedbackTagUncertaintyHandling,
		Title:       "Handle uncertainty explicitly",
		Description: "State confidence and missing evidence when the available output is incomplete or ambiguous.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingStateMissingEvidence,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "State missing evidence",
		Description: "If evidence is incomplete, say what was not observed and what additional read-only evidence would clarify it.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingAvoidOverclaiming,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "Avoid overclaiming",
		Description: "Avoid asserting compromise, causality, or success/failure beyond what the observed outputs support.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingExplainLimitations,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "Explain local limitations",
		Description: "State tool or evidence limitations plainly, and phrase conclusions with the appropriate confidence level.",
	},

	{
		ID:          FeedbackTagConcision,
		Title:       "Keep improvements concise",
		Description: "Make one concise, general improvement instead of adding long or special-case guidance.",
	},
	{
		ID:          FeedbackTagConcisionOneSmallGeneralChange,
		ParentID:    FeedbackTagConcision,
		Title:       "Make one small general change",
		Description: "Prefer a single broadly useful skill edit over multiple speculative additions in one iteration.",
	},
	{
		ID:          FeedbackTagConcisionAvoidSpecialCaseRules,
		ParentID:    FeedbackTagConcision,
		Title:       "Avoid special-case rules",
		Description: "Do not add narrow rules that encode one benchmark's wording, data shape, identifiers, or expected answer.",
	},
}

var (
	feedbackTagDescriptions = map[string]string{}
	feedbackTagTitles       = map[string]string{}
	feedbackTagParents      = map[string]string{}
	feedbackTagOrder        []string
)

func init() {
	feedbackTagOrder = make([]string, 0, len(feedbackCards))
	for _, card := range feedbackCards {
		feedbackTagOrder = append(feedbackTagOrder, card.ID)
		feedbackTagDescriptions[card.ID] = card.Description
		feedbackTagTitles[card.ID] = card.Title
		feedbackTagParents[card.ID] = card.ParentID
	}
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

// FeedbackTagTitle returns the generic card title for a tag.
func FeedbackTagTitle(tag string) (string, bool) {
	title, ok := feedbackTagTitles[strings.TrimSpace(tag)]
	return title, ok
}

// FeedbackTagParent returns the immediate parent tag for a granular tag.
func FeedbackTagParent(tag string) (string, bool) {
	parent, ok := feedbackTagParents[strings.TrimSpace(tag)]
	return parent, ok
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
	return feedbackTagsInOrder(seen)
}

// ExpandFeedbackTags returns normalized tags plus their parent tags. It is used
// for aggregate disclosure so granular cards can be shown when recurring, while
// parent cards remain available as a safe fallback for sparse child signals.
func ExpandFeedbackTags(tags []string) []string {
	seen := map[string]bool{}
	for _, tag := range NormalizeFeedbackTags(tags) {
		seen[tag] = true
		for {
			parent := feedbackTagParents[tag]
			if parent == "" || seen[parent] {
				break
			}
			seen[parent] = true
			tag = parent
		}
	}
	return feedbackTagsInOrder(seen)
}

func feedbackTagsInOrder(seen map[string]bool) []string {
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
