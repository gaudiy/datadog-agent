// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build serverless

package listeners

import (
	"github.com/DataDog/datadog-agent/comp/core/workloadmeta"
	"github.com/DataDog/datadog-agent/pkg/util/optional"
)

type noopServiceListenerFactory func(c Config, wmeta optional.Option[workloadmeta.Component]) (ServiceListener, error)

var NewContainerListener noopServiceListenerFactory
