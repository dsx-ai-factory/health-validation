// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type streamPacket struct {
	err  error
	body string
}

type streamReader struct {
	packets chan streamPacket
	closed  chan struct{}
	once    sync.Once
}

func (r *streamReader) Read(buf []byte) (int, error) {
	select {
	case <-r.closed:
		return 0, os.ErrClosed
	case packet := <-r.packets:
		return copy(buf, packet.body), packet.err
	}
}
func (r *streamReader) Close() error { r.once.Do(func() { close(r.closed) }); return nil }

func TestStreamSharedWindow(t *testing.T) {
	r := &streamReader{packets: make(chan streamPacket, 16), closed: make(chan struct{})}
	s := startStream(r, time.Unix(1000, 0), "boot", 2)
	defer func() { _ = s.Close() }()
	r.packets <- streamPacket{body: "6,1,100,-;same"}
	r.packets <- streamPacket{body: "6,2,200,-;same"}
	require.Eventually(t, func() bool { msgs, _ := s.Read(context.Background()); return len(msgs) == 2 }, time.Second, time.Millisecond)
	first, err := s.Read(context.Background())
	require.NoError(t, err)
	second, err := s.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, first, second)
	first[0].Body = "mutated"
	require.Equal(t, "same", second[0].Body)
	require.Equal(t, "boot", second[0].BootID)
	r.packets <- streamPacket{body: "6,3,300,-;third"}
	require.Eventually(t, func() bool { msgs, _ := s.Read(context.Background()); return len(msgs) == 2 && msgs[1].Sequence == 3 }, time.Second, time.Millisecond)
	latest, err := s.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, latest[0].Sequence)
	require.Equal(t, "same", latest[0].Body)
	r.packets <- streamPacket{err: syscall.EPIPE}
	r.packets <- streamPacket{body: "6,4,400,-;fourth"}
	require.Eventually(t, func() bool {
		msgs, readErr := s.Read(context.Background())
		return errors.Is(readErr, syscall.EPIPE) && len(msgs) == 2 && msgs[1].Sequence == 4
	}, time.Second, time.Millisecond)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	retained, readErr := s.Read(context.Background())
	require.Len(t, retained, 2)
	require.ErrorIs(t, readErr, os.ErrClosed)
	require.ErrorIs(t, readErr, syscall.EPIPE)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, readErr = s.Read(ctx)
	require.ErrorIs(t, readErr, context.Canceled)
}

func TestReadFollowGPUdBehavior(t *testing.T) {
	for _, terminal := range []error{io.EOF, os.ErrClosed, syscall.EIO} {
		r := &streamReader{packets: make(chan streamPacket, 8), closed: make(chan struct{})}
		for _, packet := range []streamPacket{
			{err: syscall.EPIPE},
			{err: syscall.EINTR},
			{},
			{body: "6,1,100,-;body;with;semicolons\n metadata"},
			{err: terminal},
		} {
			r.packets <- packet
		}
		var emitted []Message
		var reported error
		err := readFollow(r, time.Unix(1000, 0), "boot", func(msg Message) { emitted = append(emitted, msg) }, func(err error) { reported = err })
		require.ErrorIs(t, err, terminal)
		require.ErrorIs(t, reported, syscall.EPIPE)
		require.Len(t, emitted, 1)
		require.Equal(t, "body;with;semicolons\n metadata", emitted[0].Body)
	}
	r := &streamReader{packets: make(chan streamPacket, 1), closed: make(chan struct{})}
	r.packets <- streamPacket{body: "malformed"}
	err := readFollow(r, time.Now(), "boot", func(Message) { t.Fatal("emitted invalid record") }, func(error) {})
	require.ErrorIs(t, err, ErrMalformedRecord)
}

func TestStreamCloseInterruptsIdleRead(t *testing.T) {
	read, write, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = write.Close() }()
	s := startStream(read, time.Now(), "boot", 2)
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case closeErr := <-done:
		require.NoError(t, closeErr)
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt idle read")
	}
}

func TestStreamConcurrentSnapshots(t *testing.T) {
	r := &streamReader{packets: make(chan streamPacket, 64), closed: make(chan struct{})}
	s := startStream(r, time.Now(), "boot", 8)
	defer func() { _ = s.Close() }()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				_, _ = s.Read(context.Background())
			}
		})
	}
	for range 64 {
		r.packets <- streamPacket{body: "6,1,100,-;body"}
	}
	wg.Wait()
}

func TestStreamInvalidCapacity(t *testing.T) {
	_, err := NewStream(-1)
	require.Error(t, err)
}
