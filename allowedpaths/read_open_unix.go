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
	file       *os.File
	done       chan struct{}
	closeOnce  sync.Once
	writerSeen bool
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
		n, err := f.file.Read(p)
		if n > 0 || errors.Is(err, syscall.EAGAIN) {
			f.writerSeen = true
		}
		switch {
		case n > 0:
			return n, err
		case errors.Is(err, io.EOF) && f.writerSeen:
			return 0, io.EOF
		case err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EAGAIN):
			return 0, err
		}
		select {
		case <-f.done:
			return 0, io.EOF
		case <-ticker.C:
		}
	}
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
