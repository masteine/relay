package protocol

import (
	"regexp"
	"strings"
)

// Command is a slash command written as an Issue comment. The bot's buttons
// only post these; a human can type the same thing in the GitHub UI.
type Command string

const (
	CmdTicket  Command = "ticket"
	CmdApprove Command = "approve"
	CmdRetry   Command = "retry"

	// v1 commands. They parse, so that v0 can answer "not yet" instead of
	// treating them as discussion text.
	CmdReject Command = "reject"
	CmdSkip   Command = "skip"
	CmdAbort  Command = "abort"
	CmdStats  Command = "stats"
	CmdStatus Command = "status"
)

var knownCommands = map[Command]bool{
	CmdTicket: true, CmdApprove: true, CmdRetry: true,
	CmdReject: true, CmdSkip: true, CmdAbort: true, CmdStats: true, CmdStatus: true,
}

// InV0 reports whether the workflow handles the command in v0.
func (c Command) InV0() bool {
	switch c {
	case CmdTicket, CmdApprove, CmdRetry:
		return true
	}
	return false
}

// ParsedCommand is a command with its argument, for example
// `/reject the plan touches the wrong module` → {reject, "the plan ..."}.
type ParsedCommand struct {
	Name Command
	Arg  string
}

var cmdRe = regexp.MustCompile(`^/([a-z]+)(?:[ \t]+(.*))?$`)

// ParseCommand reads a command from the first non-blank line of a comment.
// Only known command names count; "/usr/bin/foo is broken" is discussion
// text, not a command.
func ParseCommand(body string) (ParsedCommand, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := cmdRe.FindStringSubmatch(line)
		if m == nil || !knownCommands[Command(m[1])] {
			return ParsedCommand{}, false
		}
		return ParsedCommand{Name: Command(m[1]), Arg: strings.TrimSpace(m[2])}, true
	}
	return ParsedCommand{}, false
}
