// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package get_winevent

import (
	"encoding/json"
	"fmt"

	"github.com/DataDog/rshell/builtins/internal/wineventlog"
)

// serializeEvent constructs a complete output record before it reaches
// stdout. The final-byte check is deliberately here, after TSV/JSON escaping
// and repeated selected columns, rather than relying on the adapter's XML
// render cap.
func serializeEvent(ev wineventlog.Event, opts options) ([]byte, error) {
	if opts.output == "tsv" {
		row := []byte(wineventlog.FormatRow(ev, opts.columns))
		if len(row) > MaxRenderBytes {
			return nil, fmt.Errorf("rendered TSV record exceeds %d bytes", MaxRenderBytes)
		}
		return row, nil
	}
	tree, err := wineventlog.EventTree(ev.Raw)
	if err != nil {
		return nil, err
	}
	tree["Message"] = ev.Message
	row, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("serialize JSON Lines record: %w", err)
	}
	row = append(row, '\n')
	if len(row) > MaxRenderBytes {
		return nil, fmt.Errorf("rendered JSON Lines record exceeds %d bytes", MaxRenderBytes)
	}
	return row, nil
}
