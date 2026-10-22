// Package gh is the GitHub side of Relay: the handful of Issue operations the
// stages and the bot need, behind an interface with an in-memory fake.
package gh

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/masteine/relay/internal/protocol"
)

// Repo identifies a repository.
type Repo struct {
	Owner string
	Name  string
}

// String renders owner/name.
func (r Repo) String() string { return r.Owner + "/" + r.Name }

var repoPartRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ParseRepo splits "owner/name". Both parts must be plain GitHub names: no
// path tricks ("..", "/"), since they end up in directory names.
func ParseRepo(full string) (Repo, error) {
	owner, name, ok := strings.Cut(full, "/")
	if !ok || !repoPartRe.MatchString(owner) || !repoPartRe.MatchString(name) || name == "." || name == ".." || owner == "." || owner == ".." {
		return Repo{}, fmt.Errorf("repository %q is not owner/name", full)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// Comment is an Issue comment as fetched from the API.
type Comment struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
}

// Issue is an Issue with all its comments.
type Issue struct {
	Number        int
	Title         string
	Body          string
	URL           string
	Labels        []string
	IsPullRequest bool
	Comments      []Comment
}

// Reaction is one reaction on a comment.
type Reaction struct {
	User    string
	Content string // "eyes", "+1", …
}

// ReactionEyes is what the bot puts on a comment it has forwarded.
const ReactionEyes = "eyes"

// Protocol converts the Issue to what the protocol package reasons about.
func (i Issue) Protocol() protocol.Issue {
	out := protocol.Issue{Number: i.Number, Labels: append([]string(nil), i.Labels...)}
	for _, c := range i.Comments {
		out.Comments = append(out.Comments, protocol.Comment{ID: c.ID, Author: c.Author, Body: c.Body})
	}
	sort.Slice(out.Comments, func(a, b int) bool { return out.Comments[a].ID < out.Comments[b].ID })
	return out
}

// Client is what a stage needs from GitHub. Every method is one API call
// (ListComments pages until the end).
type Client interface {
	// IsPrivate reports the repository's visibility.
	IsPrivate(ctx context.Context) (bool, error)
	// Issue fetches an Issue with all its comments.
	Issue(ctx context.Context, number int) (Issue, error)
	// CreateComment posts a comment and returns its id.
	CreateComment(ctx context.Context, number int, body string) (int64, error)
	// EditIssue replaces the title and the body. An empty title keeps it.
	EditIssue(ctx context.Context, number int, title, body string) error
	// AddLabels adds labels; existing ones are left alone.
	AddLabels(ctx context.Context, number int, labels []string) error
	// RemoveLabel removes one label. A label that is not there is not an
	// error.
	RemoveLabel(ctx context.Context, number int, label string) error
	// FileContent reads a file from the default branch. Jobs that have no
	// checkout (route, report-failure) read relay.yml this way.
	FileContent(ctx context.Context, path string) (string, error)

	// The bot's side.

	// Login is the login the token belongs to.
	Login(ctx context.Context) (string, error)
	// CreateIssue opens an Issue and returns it (number and URL set).
	CreateIssue(ctx context.Context, title, body string, labels []string) (Issue, error)
	// ListUpdated returns Issues (not pull requests) updated since the
	// given time, without comments, newest first.
	ListUpdated(ctx context.Context, since time.Time) ([]Issue, error)
	// Reactions lists the reactions on a comment.
	Reactions(ctx context.Context, commentID int64) ([]Reaction, error)
	// React adds a reaction to a comment.
	React(ctx context.Context, commentID int64, content string) error
	// ActiveRuns counts queued and running runs of a workflow file.
	ActiveRuns(ctx context.Context, workflowFile string) (int, error)
}

// ApplyLabels moves an Issue from cur to next with the smallest set of API
// calls: additions first, so that the Issue never has zero state labels in
// between.
func ApplyLabels(ctx context.Context, c Client, number int, cur, next protocol.Labels) error {
	add, remove := protocol.Diff(cur, next)
	if len(add) > 0 {
		if err := c.AddLabels(ctx, number, add); err != nil {
			return fmt.Errorf("add labels %v: %w", add, err)
		}
	}
	for _, l := range remove {
		if err := c.RemoveLabel(ctx, number, l); err != nil {
			return fmt.Errorf("remove label %s: %w", l, err)
		}
	}
	return nil
}
