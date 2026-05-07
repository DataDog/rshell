// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"errors"
	"os"
	"path/filepath"
)

// errNoSandbox is returned by changeDir when no AllowedPaths sandbox has
// been configured. With no sandbox, all file access is blocked, so cd
// has nowhere safe to land.
var errNoSandbox = errors.New("no allowed paths configured")

// errInvalidPath is returned when changeDir is called with a relative
// path. The cd builtin always passes absolute paths; a relative path
// here would indicate a programming error in the caller.
var errInvalidPath = errors.New("path must be absolute")

// errNotDirectory is returned when the target exists but is not a
// directory.
var errNotDirectory = errors.New("not a directory")

// changeDir mutates the runner's working directory after validating the
// supplied path. It is exposed to the cd builtin via CallContext.ChangeDir.
//
// absDir must be absolute. The directory must exist, be a directory, and
// resolve inside an AllowedPaths root. On success, $OLDPWD is set to the
// previous working directory and $PWD is set to absDir.
//
// On any failure (path not absolute, no sandbox, target missing or not a
// directory, target outside the sandbox) the working directory is left
// unchanged and an error is returned.
func (r *Runner) changeDir(absDir string) error {
	if !filepath.IsAbs(absDir) {
		return &os.PathError{Op: "chdir", Path: absDir, Err: errInvalidPath}
	}
	if r.sandbox == nil {
		return &os.PathError{Op: "chdir", Path: absDir, Err: errNoSandbox}
	}
	cleaned := filepath.Clean(absDir)
	info, err := r.sandbox.Stat(cleaned, r.Dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: absDir, Err: errNotDirectory}
	}
	// bash's cd refuses to enter a directory the user lacks search
	// (execute) permission on. Stat alone does not check this, so verify
	// access through the sandbox before committing the change. The
	// 0x01 mode is the same execute bit accepted by AccessFile callers.
	if err := r.sandbox.Access(cleaned, r.Dir, 0x01); err != nil {
		return err
	}

	oldDir := r.Dir
	r.Dir = cleaned
	// Use setVarString so the new values count toward MaxVarBytes /
	// MaxTotalVarsBytes the same way as a script-driven assignment.
	// setVarString records storage-cap failures on the runner's exit
	// state but does not return an error here; that's intentional —
	// matching bash, the dir change is committed even if the env-var
	// update could not be persisted.
	r.setVarString("OLDPWD", oldDir)
	r.setVarString("PWD", cleaned)
	return nil
}

// lookupEnvVar returns the value of an environment variable in the
// runner's overlay environment, or ("", false) if unset. Used by the cd
// builtin to read $HOME and $OLDPWD without exposing the full
// WriteEnviron handle to every CallContext.
func (r *Runner) lookupEnvVar(name string) (string, bool) {
	vr := r.writeEnv.Get(name)
	if !vr.IsSet() {
		return "", false
	}
	return vr.Str, true
}
