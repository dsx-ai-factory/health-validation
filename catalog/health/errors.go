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

package health

import "errors"

// ErrCollectionTimeout marks a measurement that failed because collecting
// it exceeded the allotted time, as opposed to a confirmed hardware or
// driver fault. The collection layer wraps it into an errored
// measurement's cause when its watchdog fires; evaluation maps it to a
// retry-collection action distinct from a fault's remediation. Match it
// with [errors.Is].
var ErrCollectionTimeout = errors.New("collection timed out")
