// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import "errors"

// metricWatch retains each owned cleanup step until it succeeds. Callers
// serialize cleanup with native operations through the source's native gate.
type metricWatch struct {
	unwatch func() error
	destroy func() error
}

func (watch *metricWatch) close() error {
	if watch.unwatch != nil {
		if err := watch.unwatch(); err != nil {
			return err
		}
		watch.unwatch = nil
	}
	if watch.destroy != nil {
		if err := watch.destroy(); err != nil {
			return err
		}
		watch.destroy = nil
	}
	return nil
}

type metricWatches []*metricWatch

func (watches metricWatches) close() error {
	var failures []error
	for _, watch := range watches {
		if err := watch.close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
