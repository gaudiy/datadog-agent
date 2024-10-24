// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024-present Datadog, Inc.

//go:build linux

// Package ebpf implements tracing network events with eBPF
package ebpf

import (
	"github.com/DataDog/datadog-agent/pkg/util/kernel"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

var (
	// PrecompiledEbpfDeprecatedKernelVersionRhel is the kernel version
	// where pre-compiled eBPF is deprecated on RHEL based kernels
	PrecompiledEbpfDeprecatedKernelVersionRhel = kernel.VersionCode(5, 14, 0)
	// PrecompiledEbpfDeprecatedKernelVersion is the kernel version
	// where pre-compiled eBPF is deprecated on non-RHEL based kernels
	PrecompiledEbpfDeprecatedKernelVersion = kernel.VersionCode(6, 0, 0)
)

// IsPrecompiledEbpfDeprecated returns true if precompiled ebpf is deprecated
// on this host
func IsPrecompiledEbpfDeprecated() bool {
	// has to be kernel 6+ or RHEL 9+ (kernel 5.14+)
	family, err := kernel.Family()
	if err != nil {
		log.Warnf("could not determine OS family: %s", err)
		return false
	}

	// check kernel version
	kv, err := kernel.HostVersion()
	if err != nil {
		log.Warnf("could not determine kernel version: %s", err)
		return false
	}

	if family == "rhel" {
		return kv >= PrecompiledEbpfDeprecatedKernelVersionRhel
	}

	return kv >= PrecompiledEbpfDeprecatedKernelVersion
}
