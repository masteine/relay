package protocol

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in   string
		want ParsedCommand
		ok   bool
	}{
		{"/ticket", ParsedCommand{Name: CmdTicket}, true},
		{"/approve\n", ParsedCommand{Name: CmdApprove}, true},
		{"  /retry  ", ParsedCommand{Name: CmdRetry}, true},
		{"\n\n/approve", ParsedCommand{Name: CmdApprove}, true},
		{"/reject the plan touches the wrong module", ParsedCommand{Name: CmdReject, Arg: "the plan touches the wrong module"}, true},
		{"/skip design", ParsedCommand{Name: CmdSkip, Arg: "design"}, true},
		{"/approve\nand some trailing text", ParsedCommand{Name: CmdApprove}, true},

		{"hello", ParsedCommand{}, false},
		{"/usr/bin/foo is broken", ParsedCommand{}, false},
		{"/unknown", ParsedCommand{}, false},
		{"/Ticket", ParsedCommand{}, false},
		{"please /approve", ParsedCommand{}, false},
		{"text\n/approve", ParsedCommand{}, false},
		{"", ParsedCommand{}, false},
	}
	for _, tc := range tests {
		got, ok := ParseCommand(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseCommand(%q) = %+v,%v want %+v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestInV0(t *testing.T) {
	for _, c := range []Command{CmdTicket, CmdApprove, CmdRetry} {
		if !c.InV0() {
			t.Errorf("%s should be in v0", c)
		}
	}
	for _, c := range []Command{CmdReject, CmdSkip, CmdAbort, CmdStats, CmdStatus} {
		if c.InV0() {
			t.Errorf("%s should not be in v0", c)
		}
	}
}
