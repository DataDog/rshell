// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"errors"
	"time"
)

// ErrSystemdUnsupported reports that the current platform cannot provide a
// requested systemd operation.
var ErrSystemdUnsupported = errors.New("systemd operation is not supported")

const (
	// MaxJournalQueryEntries is the hard per-invocation entry bound shared by
	// journal builtins and backends.
	MaxJournalQueryEntries = 1000
	// MaxJournalQueryUnits bounds exact unit scopes before any backend work.
	MaxJournalQueryUnits = 32
	// MaxSystemServiceOperands bounds exact unit selectors accepted by one
	// systemctl invocation, including the configured readable set used by
	// list-units.
	MaxSystemServiceOperands = 32
	// MaxSystemServiceNameBytes matches systemd's maximum unit-name payload
	// (UNIT_NAME_MAX minus the terminating NUL).
	MaxSystemServiceNameBytes = 255
	// MaxSystemServiceFieldBytes bounds every string returned by a manager
	// backend before it reaches command formatting.
	MaxSystemServiceFieldBytes = 64 * 1024
	// MaxSystemServicePropertyPairs bounds the number of KEY=VALUE property
	// assignments accepted by one "systemctl set-property" invocation.
	MaxSystemServicePropertyPairs = 32
	// MaxSystemServicePropertyNameBytes bounds a single property name passed
	// to "systemctl set-property".
	MaxSystemServicePropertyNameBytes = 128
	// MaxSystemServicePropertyValueBytes bounds a single scalar property
	// value, or one element of an array property value, passed to
	// "systemctl set-property".
	MaxSystemServicePropertyValueBytes = 4096
	// MaxSystemServicePropertyArrayElements bounds the number of elements in
	// an array-valued property passed to "systemctl set-property".
	MaxSystemServicePropertyArrayElements = 64
)

// SystemServiceAction identifies an operation that a builtin may perform on
// an explicitly configured systemd unit. The historical "Service" name is
// retained for API compatibility; grants may name any exact unit type, such
// as .service, .timer, or .socket.
type SystemServiceAction string

const (
	SystemServiceRead        SystemServiceAction = "read"
	SystemServiceClean       SystemServiceAction = "clean"
	SystemServiceStart       SystemServiceAction = "start"
	SystemServiceStop        SystemServiceAction = "stop"
	SystemServiceReload      SystemServiceAction = "reload"
	SystemServiceRestart     SystemServiceAction = "restart"
	SystemServiceEnable      SystemServiceAction = "enable"
	SystemServiceDisable     SystemServiceAction = "disable"
	SystemServiceSetProperty SystemServiceAction = "set-property"
	// SystemServiceDaemonReload authorizes the global manager reload
	// ("systemctl daemon-reload"). It is granted against
	// [SystemdManagerService] rather than an individual unit, since the
	// operation is not scoped to any single unit.
	SystemServiceDaemonReload SystemServiceAction = "daemon-reload"
)

// IsSupportedSystemdUnitType reports whether unitType is part of the fixed
// systemd unit-type surface shared by systemctl and its manager backend.
func IsSupportedSystemdUnitType(unitType string) bool {
	switch unitType {
	case "service", "socket", "target", "device", "mount", "automount", "swap", "timer", "path", "slice", "scope":
		return true
	default:
		return false
	}
}

// SystemServiceState is the fixed, bounded unit state exposed to the restricted
// systemctl builtin. The historical "Service" name is retained for API
// compatibility. Name preserves the exact authorized selector; CanonicalName
// is used only to validate manager replies and is not an arbitrary D-Bus
// object path.
type SystemServiceState struct {
	Name          string
	CanonicalName string
	Description   string
	LoadState     string
	ActiveState   string
	SubState      string
	UnitFileState string
	MainPID       uint32
	Result        string
	JobID         uint32
}

// SystemServiceListRequest selects exact pre-authorized units for bounded
// list-units output. IncludeInactive permits loading inactive configured
// units; false restricts the result to units already loaded by systemd.
type SystemServiceListRequest struct {
	Services        []string
	IncludeInactive bool
}

// SystemServiceStateReader exposes fixed unit state without a generic
// property, object-path, or transport API.
type SystemServiceStateReader interface {
	ListSystemServices(ctx context.Context, request SystemServiceListRequest) ([]SystemServiceState, error)
	InspectSystemServices(ctx context.Context, services []string) ([]SystemServiceState, error)
}

// SystemServiceJobAction is the fixed set of runtime jobs exposed by the
// restricted systemctl backend.
type SystemServiceJobAction string

const (
	SystemServiceJobStart   SystemServiceJobAction = "start"
	SystemServiceJobStop    SystemServiceJobAction = "stop"
	SystemServiceJobReload  SystemServiceJobAction = "reload"
	SystemServiceJobRestart SystemServiceJobAction = "restart"
)

// SystemServiceController performs only fixed unit operations. Job methods
// return after systemd reports completion for every requested unit.
type SystemServiceController interface {
	RunSystemServiceJobs(ctx context.Context, action SystemServiceJobAction, services []string) error
	EnableSystemServices(ctx context.Context, services []string) error
	DisableSystemServices(ctx context.Context, services []string) error
	// SetUnitProperties applies properties to service through the manager's
	// SetUnitProperties method ("systemctl set-property"). When runtime is
	// true, the change applies only until the next reboot/reload; otherwise
	// it is also persisted to a unit-file drop-in.
	SetUnitProperties(ctx context.Context, service string, runtime bool, properties []SystemServiceProperty) error
	// ReloadManager re-reads every unit file on the host
	// ("systemctl daemon-reload"). It is not scoped to any single unit.
	ReloadManager(ctx context.Context) error
}

// SystemServicePropertyKind selects how a [SystemServiceProperty] value is
// marshaled to the manager. Callers cannot supply an arbitrary D-Bus
// signature; only this fixed set of scalar and array kinds is accepted.
type SystemServicePropertyKind string

const (
	SystemServicePropertyString      SystemServicePropertyKind = "string"
	SystemServicePropertyUint64      SystemServicePropertyKind = "uint64"
	SystemServicePropertyBool        SystemServicePropertyKind = "bool"
	SystemServicePropertyStringArray SystemServicePropertyKind = "string-array"
)

// SystemServiceProperty is one name/value pair passed to
// SystemServiceController.SetUnitProperties. Exactly one value field is used,
// selected by Kind.
type SystemServiceProperty struct {
	Name             string
	Kind             SystemServicePropertyKind
	StringValue      string
	Uint64Value      uint64
	BoolValue        bool
	StringArrayValue []string
}

// JournalQuery is the bounded, structured query accepted by the trusted
// journal backend. Callers cannot provide raw journal matches or paths.
type JournalQuery struct {
	Units       []string
	Kernel      bool
	CurrentBoot bool
	Since       time.Time
	MaxEntries  int
}

// JournalEntry contains only the fields a journalctl builtin may expose. The
// backend deliberately does not return arbitrary journal fields.
type JournalEntry struct {
	Timestamp  time.Time
	Hostname   string
	Identifier string
	PID        string
	Message    string
}

// JournalReader reads a bounded journal query and yields entries oldest first.
type JournalReader interface {
	ReadJournal(ctx context.Context, query JournalQuery, yield func(JournalEntry) error) error
}

// JournalUsage is the allocated storage consumed by the selected target's
// active and archived journal files.
type JournalUsage struct {
	Bytes uint64
	Files int
}

// JournalStorageReader exposes read-only journal storage metadata.
type JournalStorageReader interface {
	JournalDiskUsage(ctx context.Context) (JournalUsage, error)
}

// JournalVacuumRequest contains only bounded cleanup predicates. Before is an
// absolute archive mtime cutoff. MaxBytes is a target for the total allocated
// journal bytes of the selected target (active plus archived, the same set
// JournalDiskUsage reports); only archives at or before the cutoff may ever be
// deleted to approach it.
type JournalVacuumRequest struct {
	Now      time.Time
	Before   time.Time
	MaxBytes uint64
	DryRun   bool
}

// JournalVacuumResult reports the cleanup selected or completed without
// exposing host paths or journal filenames. RemainingBytes is the total
// allocated journal storage that remains (or would remain, for a dry run)
// after the reported deletions, so callers can see when a size target could
// not be reached without deleting protected files.
type JournalVacuumResult struct {
	Files          int
	Bytes          uint64
	RemainingBytes uint64
}

// JournalCleaner performs request-bounded cleanup of archived journal files.
type JournalCleaner interface {
	VacuumJournal(ctx context.Context, request JournalVacuumRequest) (JournalVacuumResult, error)
}

// JournalRotator synchronously archives the active journals for the selected
// target. Implementations return only after journald reports completion.
type JournalRotator interface {
	RotateJournal(ctx context.Context) error
}

// SystemdServices contains the trusted backends available to systemd-aware
// builtins. Additional manager and journal-maintenance interfaces can be added
// here without exposing transports to command implementations.
type SystemdServices struct {
	Journal        JournalReader
	JournalStorage JournalStorageReader
	JournalCleaner JournalCleaner
	JournalRotator JournalRotator
	ServiceState   SystemServiceStateReader
	ServiceControl SystemServiceController
}
