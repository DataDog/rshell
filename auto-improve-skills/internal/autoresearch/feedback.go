// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package autoresearch

import (
	"fmt"
	"strings"
)

// Feedback tags are the closed, researcher-visible feedback vocabulary used by
// skilltrain. Benchmark failures and recent run artifacts may contain private
// case names, paths, expected answers, or other overfitting hazards, so the
// training loop reduces them to these approved generic cards before prompting a
// researcher. Granular cards make the guidance actionable, while parent rollups
// provide safe fallback when a theme recurs without enough child-specific signal.
// The rendered sanitized feedback must come only from these titles/descriptions,
// never from raw benchmark criteria, prompts, logs, or model outputs.
const (
	FeedbackTagScopedAccess          = "scoped_access"
	FeedbackTagBoundedInspection     = "bounded_inspection"
	FeedbackTagCommandDiscovery      = "command_discovery"
	FeedbackTagDiagnosticCorrelation = "diagnostic_correlation"
	FeedbackTagEvidenceGrounding     = "evidence_grounding"
	FeedbackTagSafeNextSteps         = "safe_next_steps"
	FeedbackTagUncertaintyHandling   = "uncertainty_handling"
	FeedbackTagConcision             = "concision"

	FeedbackTagScopedAccessUsePromptRoots           = "scoped_access.use_prompt_roots"
	FeedbackTagScopedAccessRequireAllowedPaths      = "scoped_access.require_allowed_paths"
	FeedbackTagScopedAccessAllowedPathsEveryCommand = "scoped_access.allowed_paths_every_command"
	FeedbackTagScopedAccessNoDirectArtifactReads    = "scoped_access.no_direct_artifact_reads"
	FeedbackTagScopedAccessInspectOnlyThroughRShell = "scoped_access.inspect_only_through_rshell"
	FeedbackTagScopedAccessNoRemoteActionClaims     = "scoped_access.no_remote_action_claims"
	FeedbackTagScopedAccessAvoidRealHostClaims      = "scoped_access.avoid_real_host_claims"
	FeedbackTagScopedAccessHandlePromptRoots        = "scoped_access.handle_prompt_roots"
	FeedbackTagScopedAccessCheckEachPromptRoot      = "scoped_access.check_each_prompt_root"

	FeedbackTagBoundedInspectionNarrowFilters               = "bounded_inspection.narrow_filters"
	FeedbackTagBoundedInspectionLimitLogReads               = "bounded_inspection.limit_log_reads"
	FeedbackTagBoundedInspectionAvoidWholeLogDumps          = "bounded_inspection.avoid_whole_log_dumps"
	FeedbackTagBoundedInspectionBoundRecursiveSearch        = "bounded_inspection.bound_recursive_search"
	FeedbackTagBoundedInspectionInspectRotationsSelectively = "bounded_inspection.inspect_rotations_selectively"
	FeedbackTagBoundedInspectionPreferCountsOverExamples    = "bounded_inspection.prefer_counts_over_examples"
	FeedbackTagBoundedInspectionStopAfterSufficientEvidence = "bounded_inspection.stop_after_sufficient_evidence"
	FeedbackTagBoundedInspectionAvoidRepeatedBroadSearches  = "bounded_inspection.avoid_repeated_broad_searches"
	FeedbackTagBoundedInspectionSummarizeInsteadOfDumping   = "bounded_inspection.summarize_instead_of_dumping"

	FeedbackTagCommandDiscoveryCheckBuiltinHelp             = "command_discovery.check_builtin_help"
	FeedbackTagCommandDiscoveryRunInitialHelp               = "command_discovery.run_initial_help"
	FeedbackTagCommandDiscoveryVerifySupportedFlags         = "command_discovery.verify_supported_flags"
	FeedbackTagCommandDiscoveryAvoidUnsupportedProcessFlags = "command_discovery.avoid_unsupported_process_flags"
	FeedbackTagCommandDiscoveryUseSupportedSocketListing    = "command_discovery.use_supported_socket_listing"
	FeedbackTagCommandDiscoveryChooseSupportedAlternative   = "command_discovery.choose_supported_alternative"
	FeedbackTagCommandDiscoveryAdaptAfterUnsupportedFlag    = "command_discovery.adapt_after_unsupported_flag"
	FeedbackTagCommandDiscoveryStateUnsupportedLimitations  = "command_discovery.state_unsupported_limitations"
	FeedbackTagCommandDiscoveryTreatSuggestionsAsHypotheses = "command_discovery.treat_suggestions_as_hypotheses"

	FeedbackTagDiagnosticCorrelationConnectSymptomToCause      = "diagnostic_correlation.connect_symptom_to_cause"
	FeedbackTagDiagnosticCorrelationTraceCausalChain           = "diagnostic_correlation.trace_causal_chain"
	FeedbackTagDiagnosticCorrelationCompareLogsAcrossLayers    = "diagnostic_correlation.compare_logs_across_layers"
	FeedbackTagDiagnosticCorrelationConfirmAffectedHealthy     = "diagnostic_correlation.confirm_affected_vs_healthy_components"
	FeedbackTagDiagnosticCorrelationDistinguishSignalFromNoise = "diagnostic_correlation.distinguish_signal_from_noise"
	FeedbackTagDiagnosticCorrelationTestAlternateHypotheses    = "diagnostic_correlation.test_alternate_hypotheses"
	FeedbackTagDiagnosticCorrelationCompareCurrentHistorical   = "diagnostic_correlation.compare_current_vs_historical_evidence"
	FeedbackTagDiagnosticCorrelationCompareSameEntity          = "diagnostic_correlation.compare_same_entity_vs_other_entities"
	FeedbackTagDiagnosticCorrelationVerifySuccessFailure       = "diagnostic_correlation.verify_success_and_failure_events"
	FeedbackTagDiagnosticCorrelationQuantifyPatterns           = "diagnostic_correlation.quantify_patterns"
	FeedbackTagDiagnosticCorrelationCorrelateTiming            = "diagnostic_correlation.correlate_timing"
	FeedbackTagDiagnosticCorrelationAvoidPatternOverfitting    = "diagnostic_correlation.avoid_pattern_overfitting"
	FeedbackTagDiagnosticCorrelationCheckFallbackRoots         = "diagnostic_correlation.check_fallback_or_alternate_roots"

	FeedbackTagEvidenceGroundingCiteKeyOutputs               = "evidence_grounding.cite_key_outputs"
	FeedbackTagEvidenceGroundingCiteSourceFiles              = "evidence_grounding.cite_source_files"
	FeedbackTagEvidenceGroundingCiteCommandsRun              = "evidence_grounding.cite_commands_run"
	FeedbackTagEvidenceGroundingTieEachClaimToEvidence       = "evidence_grounding.tie_each_claim_to_evidence"
	FeedbackTagEvidenceGroundingSeparateObservedFromInferred = "evidence_grounding.separate_observed_from_inferred"
	FeedbackTagEvidenceGroundingSupportCountsWithCommands    = "evidence_grounding.support_counts_with_commands"
	FeedbackTagEvidenceGroundingSupportNegativeFindings      = "evidence_grounding.support_negative_findings_with_searches"
	FeedbackTagEvidenceGroundingQuoteSalientTokens           = "evidence_grounding.quote_salient_error_tokens"
	FeedbackTagEvidenceGroundingCiteRedHerringEvidence       = "evidence_grounding.cite_red_herring_evidence"
	FeedbackTagEvidenceGroundingCiteEnoughNotEverything      = "evidence_grounding.cite_enough_not_everything"

	FeedbackTagSafeNextStepsReadOnlyFollowups            = "safe_next_steps.read_only_followups"
	FeedbackTagSafeNextStepsAvoidRemediationCommands     = "safe_next_steps.avoid_remediation_commands"
	FeedbackTagSafeNextStepsAvoidRestartKillDeleteApply  = "safe_next_steps.avoid_restart_kill_delete_apply"
	FeedbackTagSafeNextStepsSeparateDiagnosticsFromFixes = "safe_next_steps.separate_diagnostics_from_fixes"
	FeedbackTagSafeNextStepsOperatorOwnsRemediation      = "safe_next_steps.operator_owns_remediation"

	FeedbackTagUncertaintyHandlingStateMissingEvidence           = "uncertainty_handling.state_missing_evidence"
	FeedbackTagUncertaintyHandlingSayUnknownWhenInsufficient     = "uncertainty_handling.say_unknown_when_insufficient"
	FeedbackTagUncertaintyHandlingStateConfidenceLevel           = "uncertainty_handling.state_confidence_level"
	FeedbackTagUncertaintyHandlingAvoidOverclaiming              = "uncertainty_handling.avoid_overclaiming"
	FeedbackTagUncertaintyHandlingAvoidUnsupportedCompromise     = "uncertainty_handling.avoid_unsupported_compromise_claims"
	FeedbackTagUncertaintyHandlingAvoidUnsupportedCausality      = "uncertainty_handling.avoid_unsupported_causality_claims"
	FeedbackTagUncertaintyHandlingAvoidDefaultNegativeConclusion = "uncertainty_handling.avoid_default_negative_conclusion"
	FeedbackTagUncertaintyHandlingExplainLimitations             = "uncertainty_handling.explain_limitations"

	FeedbackTagConcisionOneSmallGeneralChange        = "concision.one_small_general_change"
	FeedbackTagConcisionAvoidSpecialCaseRules        = "concision.avoid_special_case_rules"
	FeedbackTagConcisionAvoidEmbeddingCaseFacts      = "concision.avoid_embedding_case_facts"
	FeedbackTagConcisionPreferChecklistOverLongRules = "concision.prefer_checklist_over_long_rules"
	FeedbackTagConcisionRemoveDuplication            = "concision.remove_duplication"
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
		ID:          FeedbackTagScopedAccessRequireAllowedPaths,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Pass allowed paths explicitly",
		Description: "When inspecting prompt-provided files or directories, include an explicit rshell allowed-path scope for those roots.",
	},
	{
		ID:          FeedbackTagScopedAccessAllowedPathsEveryCommand,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Keep allowed paths on every probe",
		Description: "Do not let later rshell commands drop the allowed-path scope after the first successful probe.",
	},
	{
		ID:          FeedbackTagScopedAccessNoDirectArtifactReads,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Avoid direct artifact reads",
		Description: "Do not inspect evaluator artifacts, fixture logs, or diagnostic data with direct workspace file reads.",
	},
	{
		ID:          FeedbackTagScopedAccessInspectOnlyThroughRShell,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Inspect evidence through rshell",
		Description: "Inspect diagnostic data through ./rshell rather than direct tools or out-of-band file access.",
	},
	{
		ID:          FeedbackTagScopedAccessNoRemoteActionClaims,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Avoid unsupported remote-action claims",
		Description: "Describe only the local rshell outputs that were observed; do not imply a separate remote-action tool unless the transcript shows it.",
	},
	{
		ID:          FeedbackTagScopedAccessAvoidRealHostClaims,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Avoid real-host contact claims",
		Description: "Avoid saying a real host was contacted when the work was limited to local fixture or log-root inspection.",
	},
	{
		ID:          FeedbackTagScopedAccessHandlePromptRoots,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Handle multiple prompt roots explicitly",
		Description: "When the prompt gives primary and alternate roots, check each named root deliberately and explain which one contained useful evidence.",
	},
	{
		ID:          FeedbackTagScopedAccessCheckEachPromptRoot,
		ParentID:    FeedbackTagScopedAccess,
		Title:       "Check each supplied root",
		Description: "If one supplied root is empty or unhelpful, probe the other prompt-provided roots before concluding evidence is absent.",
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
		ID:          FeedbackTagBoundedInspectionAvoidWholeLogDumps,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Avoid whole-log dumps",
		Description: "Do not cat or emit entire diagnostic logs; use filters, counts, and small excerpts instead.",
	},
	{
		ID:          FeedbackTagBoundedInspectionBoundRecursiveSearch,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Bound recursive search",
		Description: "Keep recursive discovery scoped to likely directories and pair it with head/count limits when output could be large.",
	},
	{
		ID:          FeedbackTagBoundedInspectionInspectRotationsSelectively,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Inspect rotations selectively",
		Description: "Check rotated or historical logs only when they answer a timing or red-herring question, and keep those reads filtered.",
	},
	{
		ID:          FeedbackTagBoundedInspectionPreferCountsOverExamples,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Prefer counts for repeated events",
		Description: "For repeated events, use bounded counts or grouping rather than many repeated example lines.",
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
		ID:          FeedbackTagBoundedInspectionSummarizeInsteadOfDumping,
		ParentID:    FeedbackTagBoundedInspection,
		Title:       "Summarize instead of dumping",
		Description: "Use compact summaries of matched evidence rather than pasting large raw outputs into the final answer.",
	},

	{
		ID:          FeedbackTagCommandDiscovery,
		Title:       "Discover command support",
		Description: "Check rshell and builtin help before assuming flags, and adapt when a familiar system-tool flag is unavailable.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryCheckBuiltinHelp,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Check builtin help",
		Description: "Run rshell help or builtin-specific help early when command availability or supported flags matter.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryRunInitialHelp,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Run initial rshell help",
		Description: "Start uncertain investigations by confirming the available rshell builtins and syntax before deeper probes.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryVerifySupportedFlags,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Verify supported flags",
		Description: "Prefer documented rshell-supported flags over familiar full-system variants, especially for compatibility-sensitive builtins.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryAvoidUnsupportedProcessFlags,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Avoid unsupported process flags",
		Description: "Do not assume process/PID flags are available for socket or process-style commands; verify before using them.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryUseSupportedSocketListing,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Use supported socket listing",
		Description: "For socket questions, use the supported listening/address flags shown by rshell help rather than full Linux parity flags.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryChooseSupportedAlternative,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Choose a supported alternative",
		Description: "When a suggested command or flag is unavailable, pivot to the nearest supported read-only command that still answers part of the question.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryAdaptAfterUnsupportedFlag,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Recover from unsupported flags",
		Description: "If a flag is unsupported, switch to a supported narrower command and state any resulting information limits.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryStateUnsupportedLimitations,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "State unsupported-output limits",
		Description: "When rshell cannot expose a requested field, say what can and cannot be collected from the supported command output.",
	},
	{
		ID:          FeedbackTagCommandDiscoveryTreatSuggestionsAsHypotheses,
		ParentID:    FeedbackTagCommandDiscovery,
		Title:       "Treat suggested commands as hypotheses",
		Description: "Treat user-suggested commands as candidates to verify against rshell help, not as guaranteed supported syntax.",
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
		ID:          FeedbackTagDiagnosticCorrelationTraceCausalChain,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Trace the causal chain",
		Description: "Link symptom, intermediate failure, and likely root driver instead of reporting isolated matched lines.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationCompareLogsAcrossLayers,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Compare relevant layers",
		Description: "Check the small set of relevant application, proxy, system, or service logs needed to confirm cross-layer consistency.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationConfirmAffectedHealthy,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Contrast affected and healthy components",
		Description: "Compare evidence for the failing path with evidence that adjacent components are healthy or unrelated.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationDistinguishSignalFromNoise,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Separate signal from noise",
		Description: "Call out unrelated or secondary errors as noise only after contrasting them with evidence for the likely cause.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationTestAlternateHypotheses,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Test alternate hypotheses",
		Description: "When the prompt suggests a theory, verify it against the evidence instead of assuming it is the cause.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationCompareCurrentHistorical,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Separate current from historical evidence",
		Description: "Use timestamps or file context to distinguish current incident evidence from older rotated or historical noise.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationCompareSameEntity,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Compare same entity versus others",
		Description: "When attributing activity, compare the same source, user, route, or component against other similar entities before concluding.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationVerifySuccessFailure,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Verify both success and failure events",
		Description: "For security or availability questions, search for both failure events and matching success/recovery events before making a yes/no claim.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationQuantifyPatterns,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Quantify recurring patterns",
		Description: "When the prompt asks for scale or frequency, use bounded counts or grouping to summarize the pattern instead of relying on examples alone.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationCorrelateTiming,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Correlate timing",
		Description: "Use event timing to connect causes, symptoms, recoveries, and red herrings without over-weighting unrelated nearby events.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationAvoidPatternOverfitting,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Avoid pattern overfitting",
		Description: "Do not force a familiar prior incident pattern onto new evidence; let the current logs determine the diagnosis.",
	},
	{
		ID:          FeedbackTagDiagnosticCorrelationCheckFallbackRoots,
		ParentID:    FeedbackTagDiagnosticCorrelation,
		Title:       "Correlate fallback roots",
		Description: "When evidence may live in an alternate or host-mounted root, correlate findings across the supplied roots instead of stopping at the first one.",
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
		ID:          FeedbackTagEvidenceGroundingCiteCommandsRun,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Name commands run",
		Description: "List the bounded rshell commands or command patterns that produced the evidence used in the final answer.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingTieEachClaimToEvidence,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Tie each claim to evidence",
		Description: "Ensure every major claim in the final answer has a nearby supporting observation or an explicit uncertainty caveat.",
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
		ID:          FeedbackTagEvidenceGroundingSupportNegativeFindings,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Support negative findings",
		Description: "Support claims that something was not observed with a targeted search for the relevant positive evidence.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingQuoteSalientTokens,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Quote salient tokens",
		Description: "Quote or paraphrase the distinctive generic error tokens that make the evidence identifiable without dumping full lines.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingCiteRedHerringEvidence,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Ground red-herring calls",
		Description: "When labeling evidence as unrelated, cite the output that shows why it is not the likely cause.",
	},
	{
		ID:          FeedbackTagEvidenceGroundingCiteEnoughNotEverything,
		ParentID:    FeedbackTagEvidenceGrounding,
		Title:       "Cite enough, not everything",
		Description: "Use a few decisive evidence bullets rather than many low-value citations.",
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
		ID:          FeedbackTagSafeNextStepsAvoidRestartKillDeleteApply,
		ParentID:    FeedbackTagSafeNextSteps,
		Title:       "Avoid restart/kill/delete/apply guidance",
		Description: "Avoid naming concrete state-changing commands in recommendations, even as suggestions to try next.",
	},
	{
		ID:          FeedbackTagSafeNextStepsSeparateDiagnosticsFromFixes,
		ParentID:    FeedbackTagSafeNextSteps,
		Title:       "Separate diagnostics from fixes",
		Description: "Frame next steps as verification or inspection, not remediation execution.",
	},
	{
		ID:          FeedbackTagSafeNextStepsOperatorOwnsRemediation,
		ParentID:    FeedbackTagSafeNextSteps,
		Title:       "Leave remediation to operators",
		Description: "If remediation is obvious, state it as operator-owned context rather than an action for the diagnostic agent to perform.",
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
		ID:          FeedbackTagUncertaintyHandlingSayUnknownWhenInsufficient,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "Say unknown when evidence is insufficient",
		Description: "When logs prove an event but not its cause, say the cause is not proven instead of filling the gap.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingStateConfidenceLevel,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "State confidence level",
		Description: "Use confidence wording that matches the evidence strength, especially for likely root causes.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingAvoidOverclaiming,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "Avoid overclaiming",
		Description: "Avoid asserting compromise, causality, or success/failure beyond what the observed outputs support.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingAvoidUnsupportedCompromise,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "Avoid unsupported compromise claims",
		Description: "Do not claim account or system compromise unless matching success evidence supports that conclusion.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingAvoidUnsupportedCausality,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "Avoid unsupported causality",
		Description: "Do not label a nearby deploy, restart, or error as causal unless evidence directly connects it to the symptom.",
	},
	{
		ID:          FeedbackTagUncertaintyHandlingAvoidDefaultNegativeConclusion,
		ParentID:    FeedbackTagUncertaintyHandling,
		Title:       "Avoid default negative conclusions",
		Description: "Do not default to 'no success' or 'not present' claims until a targeted search for positive evidence has been checked.",
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
	{
		ID:          FeedbackTagConcisionAvoidEmbeddingCaseFacts,
		ParentID:    FeedbackTagConcision,
		Title:       "Avoid embedding case facts",
		Description: "Keep skill guidance generic; do not add exact paths, identifiers, timestamps, log snippets, line numbers, or expected answers.",
	},
	{
		ID:          FeedbackTagConcisionPreferChecklistOverLongRules,
		ParentID:    FeedbackTagConcision,
		Title:       "Prefer short checklist guidance",
		Description: "Use compact workflow reminders instead of long prose that the researcher may overfit.",
	},
	{
		ID:          FeedbackTagConcisionRemoveDuplication,
		ParentID:    FeedbackTagConcision,
		Title:       "Remove duplicated guidance",
		Description: "If adding a new reminder, fold it into existing guidance rather than repeating the same rule in multiple places.",
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
