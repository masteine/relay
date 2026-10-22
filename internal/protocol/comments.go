package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// BotLogin is the author of every stage comment. Comments by this login never
// start a workflow run, which is what keeps the pipeline free of loops.
const BotLogin = "github-actions[bot]"

const headingPrefix = "## Relay · "

// Heading is the first line of a stage comment. The bot and humans find stage
// comments by it, so it never changes shape.
func Heading(s Stage) string {
	return headingPrefix + string(s)
}

// ParseHeading reads the stage from the first line of a comment. Any single
// word is accepted as a stage, so that comments from a newer protocol still
// count as Relay comments.
func ParseHeading(body string) (Stage, bool) {
	first, _, _ := strings.Cut(strings.TrimLeft(body, "\r\n"), "\n")
	first = strings.TrimRight(first, "\r \t")
	if !strings.HasPrefix(first, headingPrefix) {
		return "", false
	}
	s := strings.TrimSpace(strings.TrimPrefix(first, headingPrefix))
	if s == "" || strings.ContainsAny(s, " \t") {
		return "", false
	}
	return Stage(s), true
}

// IsRelayComment reports whether body starts with a Relay heading.
func IsRelayComment(body string) bool {
	_, ok := ParseHeading(body)
	return ok
}

// Marker names. Each marker is an HTML comment holding one JSON object:
//
//	<!-- relay-usage {"v":1,"stage":"plan","turns":40,...} -->
//
// Markers live in a block at the end of the comment. Only that block is
// parsed, and within it the last marker of a name wins, so text in the body
// can never override a comment's own markers.
const (
	markerUsage   = "relay-usage"
	markerSession = "relay-session"
	markerError   = "relay-error"
	markerAck     = "relay-ack"
)

// Usage is the cost footer of a stage, in machine-readable form. Token counts
// are the sum over all models, including subagents.
type Usage struct {
	V           int     `json:"v"`
	Stage       Stage   `json:"stage"`
	Turns       int     `json:"turns"`
	In          int64   `json:"in"`
	Out         int64   `json:"out"`
	CacheRead   int64   `json:"cache_read"`
	CacheCreate int64   `json:"cache_create"`
	Ms          int64   `json:"ms"`
	USD         float64 `json:"usd"`
	Model       string  `json:"model"`
}

// Session links an Issue's discussion to a Claude Code session id.
type Session struct {
	V  int    `json:"v"`
	ID string `json:"id"`
}

// ErrorMarker records which stage failed, so that /retry knows where to
// resume. Run is the GITHUB_RUN_ID that reported it; report-failure uses it
// to avoid posting twice for one run.
type ErrorMarker struct {
	V      int    `json:"v"`
	Stage  Stage  `json:"stage"`
	Reason string `json:"reason,omitempty"`
	Run    string `json:"run,omitempty"`
}

// Ack is the watermark of processed input. Upto is the highest comment id the
// stage fed to Claude; reconcile only looks at comments above it. Run is the
// GITHUB_RUN_ID of the run that posted the comment.
type Ack struct {
	V    int    `json:"v"`
	Upto int64  `json:"upto"`
	Run  string `json:"run,omitempty"`
}

var markerRe = regexp.MustCompile(`<!--\s*(relay-[a-z]+)\s+(\{.*?\})\s*-->`)

func formatMarker(name string, v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// JSON escapes "<" and ">" either way; keeping HTML escaping on makes
	// sure "-->" can never appear inside a marker.
	enc.SetEscapeHTML(true)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return fmt.Sprintf("<!-- %s %s -->", name, strings.TrimRight(buf.String(), "\n")), nil
}

// splitMarkers separates a comment into its text and the trailing block of
// marker lines. A marker line holds nothing but markers and whitespace.
func splitMarkers(body string) (text, block string) {
	lines := strings.Split(strings.TrimRight(body, "\r\n \t"), "\n")
	i := len(lines)
	for i > 0 {
		l := strings.TrimSpace(lines[i-1])
		if l == "" || isMarkerLine(l) {
			i--
			continue
		}
		break
	}
	return strings.Join(lines[:i], "\n"), strings.Join(lines[i:], "\n")
}

func isMarkerLine(l string) bool {
	return markerRe.MatchString(l) && strings.TrimSpace(markerRe.ReplaceAllString(l, "")) == ""
}

func parseMarker(body, name string, v any) (bool, error) {
	_, block := splitMarkers(body)
	var raw string
	for _, m := range markerRe.FindAllStringSubmatch(block, -1) {
		if m[1] == name {
			raw = m[2]
		}
	}
	if raw == "" {
		return false, nil
	}
	var ver struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal([]byte(raw), &ver); err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	if ver.V > Version {
		return false, fmt.Errorf("%s: protocol v%d is newer than this binary's v%d", name, ver.V, Version)
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return true, nil
}

// ParseUsage reads the relay-usage marker. ok is false when there is none.
func ParseUsage(body string) (u Usage, ok bool, err error) {
	ok, err = parseMarker(body, markerUsage, &u)
	return
}

// ParseSession reads the relay-session marker.
func ParseSession(body string) (s Session, ok bool, err error) {
	ok, err = parseMarker(body, markerSession, &s)
	return
}

// ParseError reads the relay-error marker.
func ParseError(body string) (e ErrorMarker, ok bool, err error) {
	ok, err = parseMarker(body, markerError, &e)
	return
}

// ParseAck reads the relay-ack marker.
func ParseAck(body string) (a Ack, ok bool, err error) {
	ok, err = parseMarker(body, markerAck, &a)
	return
}

// StageComment is one comment a stage posts. Every stage comment carries an
// Ack; the other markers depend on the stage.
type StageComment struct {
	Stage   Stage
	Body    string
	Ack     Ack
	Usage   *Usage
	Session *Session
	Error   *ErrorMarker
}

// FormatStageComment renders the comment:
//
//	## Relay · <stage>
//
//	<body>
//
//	---
//	<human footer, only with Usage>
//
//	<!-- relay-ack {...} -->
//	<!-- relay-usage {...} -->   (optional)
//	<!-- relay-session {...} --> (optional)
//	<!-- relay-error {...} -->   (optional)
//
// "<!--" in the body is rendered as "&lt;!--", so that a body quoting a marker
// shows it as text instead of adding one.
func FormatStageComment(c StageComment) (string, error) {
	if c.Stage == "" {
		return "", fmt.Errorf("stage comment without a stage")
	}
	var b strings.Builder
	b.WriteString(Heading(c.Stage))
	b.WriteString("\n\n")
	if body := strings.TrimSpace(c.Body); body != "" {
		b.WriteString(strings.ReplaceAll(body, "<!--", "&lt;!--"))
		b.WriteString("\n\n")
	}
	if c.Usage != nil {
		b.WriteString("---\n")
		b.WriteString(UsageFooter(*c.Usage))
		b.WriteString("\n\n")
	}
	ack := c.Ack
	if ack.V == 0 {
		ack.V = Version
	}
	type marker struct {
		name string
		v    any
	}
	markers := []marker{{markerAck, ack}}
	if c.Usage != nil {
		u := *c.Usage
		if u.V == 0 {
			u.V = Version
		}
		if u.Stage == "" {
			u.Stage = c.Stage
		}
		markers = append(markers, marker{markerUsage, u})
	}
	if c.Session != nil {
		s := *c.Session
		if s.V == 0 {
			s.V = Version
		}
		markers = append(markers, marker{markerSession, s})
	}
	if c.Error != nil {
		e := *c.Error
		if e.V == 0 {
			e.V = Version
		}
		markers = append(markers, marker{markerError, e})
	}
	for i, m := range markers {
		line, err := formatMarker(m.name, m.v)
		if err != nil {
			return "", err
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
	}
	b.WriteString("\n")
	return b.String(), nil
}

// ParseStageComment reads a comment back. Body is the text between the
// heading and the footer or markers, as rendered (so "&lt;!--" stays
// escaped). A comment without a heading is an error; one without an Ack is
// accepted, with Ack zeroed, so that comments from an older protocol version
// still parse.
func ParseStageComment(body string) (StageComment, error) {
	var c StageComment
	stage, ok := ParseHeading(body)
	if !ok {
		return c, fmt.Errorf("not a Relay comment")
	}
	c.Stage = stage
	if a, ok, err := ParseAck(body); err != nil {
		return c, err
	} else if ok {
		c.Ack = a
	}
	if u, ok, err := ParseUsage(body); err != nil {
		return c, err
	} else if ok {
		c.Usage = &u
	}
	if s, ok, err := ParseSession(body); err != nil {
		return c, err
	} else if ok {
		c.Session = &s
	}
	if e, ok, err := ParseError(body); err != nil {
		return c, err
	} else if ok {
		c.Error = &e
	}
	text, _ := splitMarkers(body)
	text = strings.TrimLeft(text, "\r\n")
	_, text, _ = strings.Cut(text, "\n")
	if c.Usage != nil {
		if i := strings.LastIndex(text, "\n---\n"); i >= 0 {
			text = text[:i]
		}
	}
	c.Body = strings.TrimSpace(text)
	return c, nil
}

// UsageFooter is the human line that ends a stage comment with usage.
func UsageFooter(u Usage) string {
	parts := []string{
		fmt.Sprintf("Ходи: %d", u.Turns),
		fmt.Sprintf("Токени: %s ↓ / %s ↑", fmtTokens(u.In), fmtTokens(u.Out)),
		fmt.Sprintf("Час: %s", fmtDuration(u.Ms)),
		fmt.Sprintf("≈$%.2f", u.USD),
	}
	if u.Model != "" {
		parts = append(parts, "Модель: "+u.Model)
	}
	return strings.Join(parts, " · ")
}

func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func fmtDuration(ms int64) string {
	s := (ms + 500) / 1000
	if s < 60 {
		return fmt.Sprintf("%d с", s)
	}
	m := (s + 30) / 60
	if m < 60 {
		return fmt.Sprintf("%d хв", m)
	}
	return fmt.Sprintf("%d год %d хв", m/60, m%60)
}
