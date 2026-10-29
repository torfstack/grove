package tray

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/torfstack/grove/internal/daemon"
)

type launchdPlist struct {
	Label            string
	ProgramArguments []string
	RunAtLoad        bool
	KeepAlive        bool
	ExitTimeOut      int
}

func parseLaunchAgent(data []byte) (launchdPlist, error) {
	if len(data) == 0 || len(data) > 64*1024 {
		return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	for {
		token, err := decoder.Token()
		if err != nil {
			return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "plist" || len(start.Attr) != 1 || start.Attr[0].Name.Local != "version" || start.Attr[0].Value != "1.0" {
			return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
		}
		break
	}
	root, err := nextPlistElement(decoder)
	if err != nil || root.Name.Local != "dict" {
		return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
	}
	values := map[string]any{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
		}
		if end, ok := token.(xml.EndElement); ok && end.Name.Local == "dict" {
			break
		}
		keyStart, ok := token.(xml.StartElement)
		if !ok || keyStart.Name.Local != "key" {
			if isWhitespace(token) {
				continue
			}
			return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
		}
		var key string
		if err := decoder.DecodeElement(&key, &keyStart); err != nil || key == "" {
			return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
		}
		if _, exists := values[key]; exists {
			return launchdPlist{}, fmt.Errorf("duplicate launch agent key")
		}
		valueStart, err := nextPlistElement(decoder)
		if err != nil {
			return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
		}
		value, err := readPlistValue(decoder, valueStart)
		if err != nil {
			return launchdPlist{}, err
		}
		values[key] = value
	}
	if _, err := nextPlistEnd(decoder, "plist"); err != nil {
		return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return launchdPlist{}, fmt.Errorf("invalid launch agent plist")
		}
		if !isWhitespace(token) {
			return launchdPlist{}, fmt.Errorf("invalid launch agent trailing data")
		}
	}
	for key := range values {
		switch key {
		case "Label", "ProgramArguments", "RunAtLoad", "KeepAlive", "ExitTimeOut":
		default:
			return launchdPlist{}, fmt.Errorf("unsupported launch agent key")
		}
	}
	plist := launchdPlist{}
	var ok bool
	if plist.Label, ok = values["Label"].(string); !ok || plist.Label == "" {
		return launchdPlist{}, fmt.Errorf("invalid launch agent label")
	}
	if plist.ProgramArguments, ok = values["ProgramArguments"].([]string); !ok || len(plist.ProgramArguments) == 0 {
		return launchdPlist{}, fmt.Errorf("invalid launch agent arguments")
	}
	if plist.RunAtLoad, ok = values["RunAtLoad"].(bool); !ok {
		return launchdPlist{}, fmt.Errorf("invalid launch agent login setting")
	}
	if value, exists := values["KeepAlive"]; exists {
		if plist.KeepAlive, ok = value.(bool); !ok {
			return launchdPlist{}, fmt.Errorf("invalid launch agent restart setting")
		}
	}
	if plist.ExitTimeOut, ok = values["ExitTimeOut"].(int); !ok {
		return launchdPlist{}, fmt.Errorf("invalid launch agent exit timeout")
	}
	return plist, nil
}

func validateLaunchAgent(data []byte, label, profileDir string) error {
	plist, err := parseLaunchAgent(data)
	if err != nil {
		return err
	}
	if plist.Label != label || !plist.RunAtLoad || plist.KeepAlive || plist.ExitTimeOut != 0 {
		return fmt.Errorf("launch agent settings do not match")
	}
	return validateLaunchInvocation(plist.ProgramArguments[0], plist.ProgramArguments, profileDir)
}

func validateLaunchInvocation(program string, args []string, profileDir string) error {
	if len(args) != 3 || args[0] != program || !filepath.IsAbs(program) || !strings.HasSuffix(program, "/groved") || args[1] != "--config" || !filepath.IsAbs(args[2]) {
		return fmt.Errorf("launch agent command does not match")
	}
	cfg, err := daemon.LoadConfig(args[2])
	if err != nil || cfg.ProfileDir != profileDir {
		return fmt.Errorf("launch agent profile does not match")
	}
	return nil
}

func nextPlistElement(decoder *xml.Decoder) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if isWhitespace(token) {
			continue
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			return xml.StartElement{}, fmt.Errorf("invalid plist element")
		}
		return start, nil
	}
}

func nextPlistEnd(decoder *xml.Decoder, name string) (xml.EndElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.EndElement{}, err
		}
		if isWhitespace(token) {
			continue
		}
		end, ok := token.(xml.EndElement)
		if !ok || end.Name.Local != name {
			return xml.EndElement{}, fmt.Errorf("invalid plist closing element")
		}
		return end, nil
	}
}

func isWhitespace(token xml.Token) bool {
	text, ok := token.(xml.CharData)
	return ok && strings.TrimSpace(string(text)) == ""
}

func readPlistValue(decoder *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "string":
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return nil, fmt.Errorf("invalid launch agent string")
		}
		return value, nil
	case "array":
		var values []string
		for {
			token, err := decoder.Token()
			if err != nil {
				return nil, fmt.Errorf("invalid launch agent array")
			}
			if end, ok := token.(xml.EndElement); ok && end.Name.Local == "array" {
				return values, nil
			}
			valueStart, ok := token.(xml.StartElement)
			if !ok {
				if isWhitespace(token) {
					continue
				}
				return nil, fmt.Errorf("invalid launch agent array")
			}
			if valueStart.Name.Local != "string" {
				return nil, fmt.Errorf("invalid launch agent array value")
			}
			var value string
			if err := decoder.DecodeElement(&value, &valueStart); err != nil || value == "" {
				return nil, fmt.Errorf("invalid launch agent array value")
			}
			values = append(values, value)
		}
	case "true", "false":
		if err := decoder.Skip(); err != nil {
			return nil, fmt.Errorf("invalid launch agent boolean")
		}
		return start.Name.Local == "true", nil
	case "integer":
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return nil, fmt.Errorf("invalid launch agent integer")
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid launch agent integer")
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("unsupported launch agent value")
	}
}
