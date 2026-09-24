// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// SPDX-FileCopyrightText: Copyright 2016 Euan Kemp
// readFollow is adapted from GPUd pkg/kmsg/watcher.go at
// 9606bb8f6813fdd944cc9a349566e35075917805 (Apache-2.0).

package kmsg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

const DefaultStreamCapacity = 4096

// Stream owns one continuous reader and a bounded, non-destructive record window.
// Share Read with all regex sensors; Close stops the reader and waits for it.
type Stream struct {
	file      io.ReadCloser
	failure   error
	closeErr  error
	done      chan struct{}
	records   []Message
	next      int
	mu        sync.RWMutex
	closeOnce sync.Once
}

// NewStream starts reading immediately, including records already in the kernel
// ring. Capacity bounds retained records; zero selects DefaultStreamCapacity.
func NewStream(capacity int) (*Stream, error) {
	if capacity < 0 {
		return nil, fmt.Errorf("kmsg stream capacity must not be negative")
	}
	if capacity == 0 {
		capacity = DefaultStreamCapacity
	}
	file, bootTime, bootID, err := openKmsg()
	if err != nil {
		return nil, err
	}
	return startStream(file, bootTime, bootID, capacity), nil
}

func startStream(file io.ReadCloser, bootTime time.Time, bootID string, capacity int) *Stream {
	s := &Stream{file: file, done: make(chan struct{}), records: make([]Message, 0, capacity)}
	go func() {
		defer close(s.done)
		err := readFollow(file, bootTime, bootID, func(msg Message) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if len(s.records) < cap(s.records) {
				s.records = append(s.records, msg)
				return
			}
			// Sensors match after Read. A new raw record can evict a match
			// before the next collection; eviction does not set an error.
			s.records[s.next] = msg
			s.next = (s.next + 1) % len(s.records)
		}, func(err error) {
			s.mu.Lock()
			s.failure = err // EPIPE is sticky: subsequent reads cannot restore lost records.
			s.mu.Unlock()
		})
		s.mu.Lock()
		s.failure = errors.Join(s.failure, err)
		s.mu.Unlock()
	}()
	return s
}

// Read returns an independent snapshot, oldest first, without consuming records.
// It can be empty during startup. After Close or reader failure, retained records
// are returned with the error. This is a window, not a lossless event queue:
// eviction keeps only the newest capacity records.
func (s *Stream) Read(ctx context.Context) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]Message, 0, len(s.records))
	records = append(records, s.records[s.next:]...)
	records = append(records, s.records[:s.next]...)
	return records, s.failure
}

func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.file.Close()
		<-s.done
	})
	return s.closeErr
}

// GPUd's follow loop, with emission to shared memory instead of a consumer
// channel. EOF/close and read failures remain visible to collection callers.
func readFollow(kmsgFile io.Reader, bootTime time.Time, bootID string, emit func(Message), report func(error)) error {
	buf := make([]byte, readBufferSize)
	for {
		// Each read call gives us one full message.
		// https://www.kernel.org/doc/Documentation/ABI/testing/dev-kmsg
		n, err := kmsgFile.Read(buf)
		switch {
		case err == nil:
			// no-op

		case errors.Is(err, syscall.EPIPE):
			report(syscall.EPIPE)
			continue

		case errors.Is(err, syscall.EINTR):
			continue

		case errors.Is(err, io.EOF),
			errors.Is(err, os.ErrClosed):
			// someone closed the kmsg file
			return err

		default:
			return fmt.Errorf("unexpected kmsg reading error: %w", err)
		}

		line := string(buf[:n])
		if len(line) == 0 {
			continue
		}

		msg, err := parseLine(bootTime, line)
		if err != nil {
			return fmt.Errorf("%w: malformed kmsg message: %w (%q)", ErrMalformedRecord, err, line)
		}

		msg.BootID = bootID

		emit(*msg)
	}
}
