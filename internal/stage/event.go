package stage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// Event is what the route command reads from the GITHUB_EVENT_PATH payload.
// It carries only the fields protocol.Route needs plus the Issue identity;
// the Issue's comments come from a fresh API fetch, never from the payload.
type Event struct {
	Name        protocol.EventName
	Action      string
	Sender      string
	RepoPrivate bool
	Repo        gh.Repo

	// IssueNumber is 0 for a workflow_dispatch without an issue input.
	IssueNumber int
	Title       string
	Body        string
	Labels      []string
	// IsPullRequest is true for an issue_comment on a pull request.
	IsPullRequest bool

	// Label is the label added, for issues.labeled.
	Label string
	// Comment is the triggering comment, for issue_comment.
	Comment *protocol.Comment
	// Dispatch holds the inputs of a workflow_dispatch run.
	Dispatch *protocol.DispatchInputs
}

// payload is the subset of a GitHub webhook payload Relay reads.
type payload struct {
	Action string `json:"action"`
	Issue  *struct {
		Number      int                     `json:"number"`
		Title       string                  `json:"title"`
		Body        string                  `json:"body"`
		Labels      []struct{ Name string } `json:"labels"`
		PullRequest *json.RawMessage        `json:"pull_request"`
	} `json:"issue"`
	Comment *struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	Label *struct {
		Name string `json:"name"`
	} `json:"label"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	Repository struct {
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
	} `json:"repository"`
	Inputs map[string]any `json:"inputs"`
}

// ParseEvent reads a payload of the named event. name is GITHUB_EVENT_NAME.
func ParseEvent(name string, data []byte) (Event, error) {
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return Event{}, fmt.Errorf("event payload: %w", err)
	}
	ev := Event{
		Name:        protocol.EventName(name),
		Action:      p.Action,
		Sender:      p.Sender.Login,
		RepoPrivate: p.Repository.Private,
	}
	repo, err := gh.ParseRepo(p.Repository.FullName)
	if err != nil {
		return Event{}, err
	}
	ev.Repo = repo

	if p.Issue != nil {
		ev.IssueNumber = p.Issue.Number
		ev.Title = p.Issue.Title
		ev.Body = p.Issue.Body
		for _, l := range p.Issue.Labels {
			ev.Labels = append(ev.Labels, l.Name)
		}
		ev.IsPullRequest = p.Issue.PullRequest != nil
	}
	if p.Comment != nil {
		ev.Comment = &protocol.Comment{ID: p.Comment.ID, Author: p.Comment.User.Login, Body: p.Comment.Body}
	}
	if p.Label != nil {
		ev.Label = p.Label.Name
	}

	switch ev.Name {
	case protocol.EventIssueComment:
		if p.Issue == nil || p.Comment == nil {
			return Event{}, fmt.Errorf("issue_comment payload without issue or comment")
		}
	case protocol.EventIssues:
		if p.Issue == nil {
			return Event{}, fmt.Errorf("issues payload without issue")
		}
	case protocol.EventWorkflowDispatch:
		d := &protocol.DispatchInputs{}
		if v, ok := p.Inputs["issue"]; ok {
			n, err := inputInt(v)
			if err != nil {
				return Event{}, fmt.Errorf("inputs.issue: %w", err)
			}
			d.Issue = n
		}
		if v, ok := p.Inputs["stage"].(string); ok {
			d.Stage = protocol.Stage(v)
		}
		ev.Dispatch = d
		ev.IssueNumber = d.Issue
	}
	return ev, nil
}

// inputInt reads a workflow_dispatch input, which GitHub delivers as a string
// for `type: number` inputs in the payload and as a number in some contexts.
func inputInt(v any) (int, error) {
	switch x := v.(type) {
	case string:
		return strconv.Atoi(strings.TrimSpace(x))
	case float64:
		return int(x), nil
	case json.Number:
		n, err := x.Int64()
		return int(n), err
	}
	return 0, fmt.Errorf("unexpected type %T", v)
}

// Webhook assembles the protocol event from the payload and the Issue as
// fetched from the API (with all its comments).
func (ev Event) Webhook(issue protocol.Issue) protocol.WebhookEvent {
	return protocol.WebhookEvent{
		Name:          ev.Name,
		Action:        ev.Action,
		Sender:        ev.Sender,
		RepoPrivate:   ev.RepoPrivate,
		Label:         ev.Label,
		IsPullRequest: ev.IsPullRequest,
		Comment:       ev.Comment,
		Issue:         issue,
		Dispatch:      ev.Dispatch,
	}
}
