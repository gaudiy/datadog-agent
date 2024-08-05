// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build linux

package modules

import (
	"errors"
	"fmt"

	"github.com/DataDog/datadog-agent/cmd/system-probe/api/module"
	"github.com/DataDog/datadog-agent/cmd/system-probe/config"
	sysconfigtypes "github.com/DataDog/datadog-agent/cmd/system-probe/config/types"
	"github.com/DataDog/datadog-agent/comp/core/tagger"
	"github.com/DataDog/datadog-agent/comp/core/telemetry"
	workloadmeta "github.com/DataDog/datadog-agent/comp/core/workloadmeta/def"
	"github.com/DataDog/datadog-agent/pkg/dynamicinstrumentation"
	"github.com/DataDog/datadog-agent/pkg/ebpf"
)

// DynamicInstrumentation is the dynamic instrumentation module factory
var DynamicInstrumentation = module.Factory{
	Name:             config.DynamicInstrumentationModule,
	ConfigNamespaces: []string{},
	Fn: func(agentConfiguration *sysconfigtypes.Config, _ workloadmeta.Component, _ telemetry.Component, _ tagger.Component) (module.Module, error) {
		config, err := dynamicinstrumentation.NewConfig(agentConfiguration)
		if err != nil {
			return nil, fmt.Errorf("invalid dynamic instrumentation module configuration: %w", err)
		}

		m, err := dynamicinstrumentation.NewModule(config)
		if errors.Is(err, ebpf.ErrNotImplemented) {
			return nil, module.ErrNotEnabled
		}

		return m, nil
	},
	NeedsEBPF: func() bool {
		return true
	},
}
