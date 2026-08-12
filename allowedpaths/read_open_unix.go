// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package allowedpaths

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"time"
)

func openReadFile(root *os.Root, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	f, err := root.OpenFile(path, flag|syscall.O_NONBLOCK, perm)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close() //nolint:errcheck
		return nil, err
	}
	if info.Mode()&fs.ModeNamedPipe == 0 {
		return f, nil
	}
	return newNonblockingFIFO(f), nil
}

type nonblockingFIFO struct {
	file      *os.File
	done      chan struct{}
	closeOnce sync.Once
}

func newNonblockingFIFO(f *os.File) *nonblockingFIFO {
	return &nonblockingFIFO{file: f, done: make(chan struct{})}
}

func (f *nonblockingFIFO) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := fifoReadReady(f.file)
		if err != nil {
			select {
			case <-f.done:
				return 0, io.EOF
			default:
				return 0, err
			}
		}
		if ready {
			n, readErr := f.file.Read(p)
			if n > 0 || (readErr != nil && !errors.Is(readErr, syscall.EAGAIN)) {
				return n, readErr
			}
		}
		select {
		case <-f.done:
			return 0, io.EOF
		case <-ticker.C:
		}
	}
}

func fifoReadReady(file *os.File) (bool, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return false, err
	}
	var ready bool
	var readyErr error
	if err := raw.Control(func(fd uintptr) {
		ready, readyErr = fifoFDReadReady(fd)
	}); err != nil {
		return false, err
	}
	if errors.Is(readyErr, syscall.EINTR) {
		return false, nil
	}
	return ready, readyErr
}

func (f *nonblockingFIFO) Write(p []byte) (int, error) {
	return f.file.Write(p)
}

func (f *nonblockingFIFO) Close() error {
	var err error
	f.closeOnce.Do(func() {
		close(f.done)
		err = f.file.Close()
	})
	return err
}
