// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"context"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type streamPacket struct {
	processed chan struct{}
	err       error
	body      string
}

type streamReader struct {
	processed chan struct{}
	packets   chan streamPacket
	closed    chan struct{}
	once      sync.Once
}

func (r *streamReader) Read(buf []byte) (int, error) {
	// Entering the next read acknowledges that the previous packet was processed.
	if r.processed != nil {
		close(r.processed)
		r.processed = nil
	}
	select {
	case <-r.closed:
		return 0, os.ErrClosed
	case packet := <-r.packets:
		r.processed = packet.processed
		return copy(buf, packet.body), packet.err
	}
}
func (r *streamReader) Close() error { r.once.Do(func() { close(r.closed) }); return nil }

func (r *streamReader) send(packet streamPacket) {
	packet.processed = make(chan struct{})
	r.packets <- packet
	<-packet.processed
}

// NewStreamForTest lets external sensor tests exercise the real follow loop.
func NewStreamForTest(t *testing.T) (*Stream, func(string, error)) {
	t.Helper()
	r := &streamReader{packets: make(chan streamPacket, 1), closed: make(chan struct{})}
	s := startStream(r, time.Unix(1000, 0), "boot", 8)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s, func(body string, err error) {
		processed := make(chan struct{})
		r.packets <- streamPacket{body: body, err: err, processed: processed}
		select {
		case <-processed:
		case <-s.done:
		}
	}
}

func TestStreamSharedWindow(t *testing.T) {
	r := &streamReader{packets: make(chan streamPacket, 16), closed: make(chan struct{})}
	s := startStream(r, time.Unix(1000, 0), "boot", 2)
	defer func() { _ = s.Close() }()
	r.send(streamPacket{body: "6,1,100,-;same"})
	r.send(streamPacket{body: "6,2,200,-;same"})
	first, err := s.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, first, 2)
	second, err := s.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, first, second)
	first[0].Body = "mutated"
	require.Equal(t, "same", second[0].Body)
	require.Equal(t, "boot", second[0].BootID)
	r.send(streamPacket{body: "6,3,300,-;third"})
	latest, err := s.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, latest, 2)
	require.Equal(t, int64(3), latest[1].Sequence)
	require.Equal(t, int64(2), latest[0].Sequence)
	require.Equal(t, "same", latest[0].Body)
	r.send(streamPacket{err: syscall.EPIPE})
	r.send(streamPacket{body: "6,4,400,-;fourth"})
	latest, err = s.Read(context.Background())
	require.ErrorIs(t, err, syscall.EPIPE)
	require.Len(t, latest, 2)
	require.Equal(t, int64(4), latest[1].Sequence)
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
	err := readFollow(r, time.Now(), "boot", func(Message) { require.FailNow(t, "emitted invalid record") }, func(error) {})
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
		require.FailNow(t, "Close did not interrupt idle read")
	}
}

func TestStreamConcurrentSnapshots(t *testing.T) {
	r := &streamReader{packets: make(chan streamPacket, 64), closed: make(chan struct{})}
	s := startStream(r, time.Now(), "boot", 8)
	defer func() { _ = s.Close() }()
	// Each round releases a writer and four snapshot readers together, then
	// waits for every reader and the committed record before the next round.
	for range 64 {
		start := make(chan struct{})
		var ready, done sync.WaitGroup
		ready.Add(5)
		for range 4 {
			done.Go(func() {
				ready.Done()
				<-start
				_, err := s.Read(context.Background())
				assert.NoError(t, err)
			})
		}
		done.Go(func() {
			ready.Done()
			<-start
			r.send(streamPacket{body: "6,1,100,-;body"})
		})
		ready.Wait()
		close(start)
		done.Wait()
	}
	messages, err := s.Read(context.Background())
	require.NoError(t, err)
	require.Len(t, messages, 8)
}

func TestStreamInvalidCapacity(t *testing.T) {
	_, err := NewStream(-1)
	require.Error(t, err)
}
