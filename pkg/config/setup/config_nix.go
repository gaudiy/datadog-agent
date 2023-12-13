// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build linux || freebsd || netbsd || openbsd || solaris || dragonfly || aix

package setup

const (
	defaultConfdPath            = "/etc/datadog-agent/conf.d"
	defaultAdditionalChecksPath = "/etc/datadog-agent/checks.d"
	defaultRunPath              = "/opt/datadog-agent/run"
	defaultGuiPort              = -1
	// DefaultSecurityAgentLogFile points to the log file that will be used by the security-agent if not configured
	DefaultSecurityAgentLogFile = "/var/log/datadog/security-agent.log"
	// DefaultAgentlessScannerLogFile points to the log file that will be used by the agentless-scanner if not configured
	DefaultAgentlessScannerLogFile = "/var/log/datadog/agentless-scanner.log"
	// DefaultProcessAgentLogFile is the default process-agent log file
	DefaultProcessAgentLogFile = "/var/log/datadog/process-agent.log"

	// defaultSystemProbeAddress is the default unix socket path to be used for connecting to the system probe
	defaultSystemProbeAddress     = "/opt/datadog-agent/run/sysprobe.sock"
	defaultSystemProbeLogFilePath = "/var/log/datadog/system-probe.log"
	// DefaultDDAgentBin the process agent's binary
	DefaultDDAgentBin = "/opt/datadog-agent/bin/agent/agent"
)

// called by init in config.go, to ensure any os-specific config is done
// in time
func osinit() {
}
