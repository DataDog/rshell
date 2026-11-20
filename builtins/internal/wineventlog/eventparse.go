// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// rawEvent matches the subset of rendered Event XML used for output fields.
// It is platform-independent: the Windows-only adapter is responsible only for
// obtaining the XML from wevtapi.
type rawEvent struct {
	XMLName xml.Name `xml:"Event"`
	System  struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		EventID     uint32 `xml:"EventID"`
		Level       uint8  `xml:"Level"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		EventRecordID uint64 `xml:"EventRecordID"`
		Channel       string `xml:"Channel"`
		Computer      string `xml:"Computer"`
		Task          uint32 `xml:"Task"`
		Opcode        uint32 `xml:"Opcode"`
		Keywords      string `xml:"Keywords"`
		Version       uint32 `xml:"Version"`
		Security      struct {
			UserID string `xml:"UserID,attr"`
		} `xml:"Security"`
		Execution struct {
			ProcessID uint32 `xml:"ProcessID,attr"`
			ThreadID  uint32 `xml:"ThreadID,attr"`
		} `xml:"Execution"`
		Correlation struct {
			ActivityID string `xml:"ActivityID,attr"`
		} `xml:"Correlation"`
	} `xml:"System"`
	EventData struct {
		Data []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"Data"`
	} `xml:"EventData"`
}

// parseEventXML extracts output fields from rendered event XML.
func parseEventXML(s string) (Event, error) {
	var r rawEvent
	if err := xml.Unmarshal([]byte(s), &r); err != nil {
		return Event{}, fmt.Errorf("parse event xml: %w", err)
	}
	data := flattenEventData(r)
	return Event{
		TimeCreated:  normalizeTime(r.System.TimeCreated.SystemTime),
		Level:        levelName(r.System.Level),
		EventID:      r.System.EventID,
		RecordID:     r.System.EventRecordID,
		ProviderName: r.System.Provider.Name,
		Message:      data,
		LogName:      r.System.Channel,
		MachineName:  r.System.Computer,
		UserID:       r.System.Security.UserID,
		ProcessID:    r.System.Execution.ProcessID,
		ThreadID:     r.System.Execution.ThreadID,
		Task:         r.System.Task,
		Opcode:       r.System.Opcode,
		Keywords:     strings.TrimSpace(r.System.Keywords),
		Version:      r.System.Version,
		ActivityID:   r.System.Correlation.ActivityID,
		EventData:    data,
	}, nil
}

// normalizeTime converts the ISO-8601 SystemTime attribute into a compact
// "2006-01-02T15:04:05Z" form. Invalid or empty inputs are returned as-is so
// the user still sees something useful.
func normalizeTime(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format("2006-01-02T15:04:05Z")
	}
	return s
}

// levelName maps Windows event level codes to their canonical names.
func levelName(level uint8) string {
	switch level {
	case 0:
		return "LogAlways"
	case 1:
		return "Critical"
	case 2:
		return "Error"
	case 3:
		return "Warning"
	case 4:
		return "Information"
	case 5:
		return "Verbose"
	default:
		return strconv.Itoa(int(level))
	}
}

// flattenEventData joins EventData/Data children into a single spaced string.
func flattenEventData(r rawEvent) string {
	var parts []string
	for _, d := range r.EventData.Data {
		if d.Value == "" {
			continue
		}
		parts = append(parts, d.Value)
	}
	return strings.Join(parts, " ")
}
