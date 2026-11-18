// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestCanceledCallOnlyBecomesStuckAtSourceTimeout(t *testing.T) {
	for _, clientDeadline := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if clientDeadline {
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			tracked, tracker := WithCallTracking(ctx)
			cycle, err := NewCycleWithTimeout("canceled-watchdog", time.Now(), 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			done := make(chan error, 1)
			go func() {
				_, runErr := Run(tracked, cycle, "native", func(context.Context) (int, error) { close(started); <-release; return 1, nil })
				done <- runErr
			}()
			<-started
			time.Sleep(time.Second)
			cancel()
			runErr := <-done
			if !errors.Is(runErr, ErrCollectionCanceled) && !errors.Is(runErr, ErrCollectionTimeout) {
				t.Fatal(runErr)
			}
			if stuck, _ := tracker.StuckCalls(); len(stuck) != 0 {
				t.Fatalf("client cancellation reported stuck calls: %v", stuck)
			}
			finished := waitTracker(t, tracker)
			synctest.Wait()
			select {
			case <-finished:
				t.Fatal("client cancellation released native ownership")
			default:
			}
			_, changed := tracker.StuckCalls()
			time.Sleep(9 * time.Second)
			synctest.Wait()
			select {
			case <-changed:
			default:
				t.Fatal("watchdog did not notify waiting clients")
			}
			if stuck, _ := tracker.StuckCalls(); len(stuck) != 1 || stuck[0].Source != "native" {
				t.Fatalf("source watchdog did not report unfinished work: %v", stuck)
			}
			once.Do(func() { close(release) })
			<-finished
			if stuck, _ := tracker.StuckCalls(); len(stuck) != 0 {
				t.Fatalf("completed call stayed stuck: %v", stuck)
			}
		})
	}
}

func TestCallFinishingBeforeWatchdogNeverBecomesStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		tracked, tracker := WithCallTracking(ctx)
		finish := TrackSourceCallWithTimeout(tracked, "native", time.Minute)
		cancel()
		time.Sleep(time.Second)
		if stuck, _ := tracker.StuckCalls(); len(stuck) != 0 {
			t.Fatal(stuck)
		}
		finish()
		tracker.Wait()
		_, changed := tracker.StuckCalls()
		time.Sleep(time.Minute)
		select {
		case <-changed:
			t.Fatal("completed call's watchdog notified observers")
		default:
		}
		if stuck, _ := tracker.StuckCalls(); len(stuck) != 0 {
			t.Fatal(stuck)
		}
	})
}
