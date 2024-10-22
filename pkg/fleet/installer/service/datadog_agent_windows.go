// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build windows

// Package service provides a way to interact with os services
package service

import (
	"context"
	"fmt"
	"github.com/DataDog/datadog-agent/pkg/fleet/internal/msilogparser"
	"github.com/DataDog/datadog-agent/pkg/fleet/internal/paths"
	"os"
	"path"

	"github.com/DataDog/datadog-agent/pkg/fleet/internal/winregistry"
	"github.com/DataDog/datadog-agent/pkg/util/log"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	datadogAgent = "datadog-agent"
)

// SetupAgent installs and starts the agent
func SetupAgent(ctx context.Context, args []string) (err error) {
	span, ctx := tracer.StartSpanFromContext(ctx, "setup_agent")
	defer func() {
		if err != nil {
			log.Errorf("Failed to setup agent: %s", err)
		}
		span.Finish(tracer.WithError(err))
	}()
	// Make sure there are no Agent already installed
	_ = removeAgentIfInstalled(ctx)
	err = installAgentPackage("stable", args)
	return err
}

// StartAgentExperiment starts the agent experiment
func StartAgentExperiment(ctx context.Context) (err error) {
	span, ctx := tracer.StartSpanFromContext(ctx, "start_experiment")
	defer func() {
		if err != nil {
			log.Errorf("Failed to start agent experiment: %s", err)
		}
		span.Finish(tracer.WithError(err))
	}()

	err = removeAgentIfInstalled(ctx)
	if err != nil {
		return err
	}

	err = installAgentPackage("experiment", nil)
	if err != nil {
		// experiment failed, expect stop-experiment to restore the stable Agent
		return err
	}
	return nil
}

// StopAgentExperiment stops the agent experiment, i.e. removes/uninstalls it.
func StopAgentExperiment(ctx context.Context) (err error) {
	span, ctx := tracer.StartSpanFromContext(ctx, "stop_experiment")
	defer func() {
		if err != nil {
			log.Errorf("Failed to stop agent experiment: %s", err)
		}
		span.Finish(tracer.WithError(err))
	}()

	err = removeAgentIfInstalled(ctx)
	if err != nil {
		return err
	}

	err = installAgentPackage(ctx, "stable", nil)
	if err != nil {
		// if we cannot restore the stable Agent, the system is left without an Agent
		return err
	}

	return nil
}

// PromoteAgentExperiment promotes the agent experiment
func PromoteAgentExperiment(_ context.Context) error {
	// noop
	return nil
}

// RemoveAgent stops and removes the agent
func RemoveAgent(ctx context.Context) (err error) {
	// Don't return an error if the Agent is already not installed.
	// returning an error here will prevent the package from being removed
	// from the local repository.
	return removeAgentIfInstalled(ctx)
}

func getMsiLogParser(logfileName string, args *[]string) (*msilogparser.MsiLogParser, string, error) {
	msiLogsDir, err := os.MkdirTemp(paths.RootTmpDir, "agent_msi_logs")
	if err != nil {
		return nil, "", err
	}
	// Don't delete dir in case we want to collect it for postmortem analysis
	//defer os.RemoveAll(msiLogsDir)
	msiLogFile := path.Join(msiLogsDir, logfileName)
	*args = append(*args, fmt.Sprintf("/log %s", msiLogFile))
	return msilogparser.NewMsiLogParser(), msiLogFile, nil
}

func installAgentPackage(ctx context.Context, target string, args []string) error {
	// Lookup stored Agent user and pass it to the Agent MSI
	// TODO: bootstrap doesn't have a command-line agent user parameter yet,
	//       might need to update this when it does.
	agentUser, err := winregistry.GetAgentUserName()
	if err != nil {
		return fmt.Errorf("failed to get Agent user: %w", err)
	}
	args = append(args, fmt.Sprintf("DDAGENTUSER_NAME=%s", agentUser))

	logParser, file, err := getMsiLogParser("install.log", &args)
	if err != nil {
		return err
	}
	cmd, err := msiexec(target, datadogAgent, "/i", args)
	if err != nil {
		return err
	}
	err = cmd.Run()
	if err != nil {
		return err
	}
	err = logParser.Parse(ctx, file)
	if err != nil {
		return err
	}
	return nil
}

func removeAgentIfInstalled(ctx context.Context) (err error) {
	if isProductInstalled("Datadog Agent") {
		span, _ := tracer.StartSpanFromContext(ctx, "remove_agent")
		defer func() {
			if err != nil {
				// removal failed, this should rarely happen.
				// Rollback might have restored the Agent, but we can't be sure.
				log.Errorf("Failed to remove agent: %s", err)
			}
			span.Finish(tracer.WithError(err))
		}()

		var args []string
		logParser, file, err := getMsiLogParser("uninstall.log", &args)
		if err != nil {
			return err
		}
		cmd, err := removeProduct("Datadog Agent", args)
		if err != nil {
			return err
		}
		err = cmd.Run()
		if err != nil {
			return err
		}
		err = logParser.Parse(ctx, file)
		if err != nil {
			return err
		}
	} else {
		log.Debugf("Agent not installed")
	}
	return nil
}
