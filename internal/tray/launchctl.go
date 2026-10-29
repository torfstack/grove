package tray

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type launchdJob struct {
	Target    string
	Label     string
	State     string
	Path      string
	Program   string
	Arguments []string
}

func parseLaunchctlJob(output []byte, label string) (launchdJob, error) {
	if len(output) == 0 || len(output) > 1024*1024 || !utf8.Valid(output) || strings.ContainsRune(string(output), '\x00') {
		return launchdJob{}, fmt.Errorf("invalid launchctl output")
	}
	lines := strings.Split(string(output), "\n")
	header := strings.TrimSpace(strings.TrimSuffix(lines[0], "\r"))
	if !strings.HasSuffix(header, " = {") {
		return launchdJob{}, fmt.Errorf("unknown launchctl header")
	}
	target := strings.TrimSuffix(header, " = {")
	parts := strings.Split(target, "/")
	if len(parts) != 3 || parts[0] != "gui" || parts[2] != label {
		return launchdJob{}, fmt.Errorf("launchctl target mismatch")
	}
	if _, err := strconv.ParseUint(parts[1], 10, 32); err != nil {
		return launchdJob{}, fmt.Errorf("invalid launchctl domain")
	}

	job := launchdJob{Target: target, Label: label}
	var sawState, sawPath, sawProgram, sawArguments bool
	depth := 1
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if depth == 0 {
			if line != "" {
				return launchdJob{}, fmt.Errorf("unexpected launchctl trailing data")
			}
			continue
		}
		if line == "}" {
			depth--
			continue
		}
		if depth != 1 {
			if strings.HasSuffix(line, " = {") {
				depth++
			}
			continue
		}
		if line != "arguments = {" && strings.HasSuffix(line, " = {") {
			depth++
			continue
		}
		if strings.HasPrefix(line, "state = ") {
			if sawState {
				return launchdJob{}, fmt.Errorf("duplicate launchctl state")
			}
			job.State = strings.TrimPrefix(line, "state = ")
			switch job.State {
			case "running", "not running", "waiting", "exited", "spawn scheduled", "throttled", "uninitialized":
			default:
				return launchdJob{}, fmt.Errorf("unknown launchctl state")
			}
			sawState = true
			continue
		}
		if strings.HasPrefix(line, "program = ") {
			if sawProgram {
				return launchdJob{}, fmt.Errorf("duplicate launchctl program")
			}
			job.Program = strings.TrimPrefix(line, "program = ")
			if job.Program == "" {
				return launchdJob{}, fmt.Errorf("empty launchctl program")
			}
			sawProgram = true
			continue
		}
		if strings.HasPrefix(line, "path = ") {
			if sawPath {
				return launchdJob{}, fmt.Errorf("duplicate launchctl path")
			}
			job.Path = strings.TrimPrefix(line, "path = ")
			if job.Path == "" {
				return launchdJob{}, fmt.Errorf("empty launchctl path")
			}
			sawPath = true
			continue
		}
		if line != "arguments = {" {
			continue
		}
		if sawArguments {
			return launchdJob{}, fmt.Errorf("duplicate launchctl arguments")
		}
		sawArguments = true
		closed := false
		for i++; i < len(lines); i++ {
			argumentLine := strings.TrimSuffix(lines[i], "\r")
			argument := strings.TrimSpace(argumentLine)
			if argument == "}" {
				closed = true
				break
			}
			argument = strings.TrimLeft(argumentLine, "\t ")
			if argument == "" || strings.TrimSpace(argument) != argument {
				return launchdJob{}, fmt.Errorf("invalid launchctl argument")
			}
			job.Arguments = append(job.Arguments, argument)
		}
		if !closed {
			return launchdJob{}, fmt.Errorf("unterminated launchctl arguments")
		}
	}
	if depth != 0 || !sawState || !sawPath || !sawProgram || !sawArguments || len(job.Arguments) == 0 || job.Arguments[0] != job.Program {
		return launchdJob{}, fmt.Errorf("incomplete launchctl job")
	}
	return job, nil
}

func launchdServiceNotFound(output []byte, label string, uid uint32) bool {
	if len(output) == 0 || len(output) > 4096 || !utf8.Valid(output) {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "Bad request." {
		return false
	}
	want := fmt.Sprintf("Could not find service %q in domain for user gui: %d", label, uid)
	return strings.TrimSpace(lines[1]) == want
}
