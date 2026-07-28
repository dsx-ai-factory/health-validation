/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package toolkit

import "github.com/NVIDIA/health-validation/catalog/health"

// Actuator executes a recommended action from a check verdict.
//
// Implementations are deployment-specific: Slurm node drain, K8s
// node cordon, FACT ticket creation, etc. The library defines this
// interface but does not ship implementations; consumers plug in
// their own.
type Actuator interface {
	// Name identifies this actuator.
	Name() string

	// Execute performs the recommended action. Called when a check
	// verdict carries a non-zero RecommendedAction.
	Execute(verdict health.CheckVerdict) error
}
