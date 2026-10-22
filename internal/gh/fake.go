package gh

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is an in-memory Client for tests. It holds one repository's Issues
// and records every write in Log, in order.
type Fake struct {
	mu      sync.Mutex
	Private bool
	Issues  map[int]*Issue
	// Files is what FileContent serves, by path.
	Files map[string]string
	// Log lists writes as "comment #n", "edit #n", "add #n label", "remove #n label".
	Log    []string
	nextID int64
	// Fail, when set, makes every call return this error.
	Fail error
	// FailOn makes one method fail, by name ("CreateComment", "AddLabels",
	// "RemoveLabel", "EditIssue", "Issue", "IsPrivate", "FileContent", …).
	FailOn map[string]error

	// The bot's side.
	Me string
	// CommentAs is the author of comments CreateComment records: the
	// workflow's bot login by default, Me when the Fake stands in for the
	// owner's token.
	CommentAs string
	Repo      Repo
	Reacts    map[int64][]Reaction
	Runs      int
	LastSince time.Time
	nextIssue int
}

// NewFake returns a private repository with the given Issues.
func NewFake(issues ...Issue) *Fake {
	f := &Fake{Private: true, Issues: map[int]*Issue{}, Files: map[string]string{}, FailOn: map[string]error{}, nextID: 1000,
		Me: "masteine", CommentAs: "github-actions[bot]", Repo: Repo{Owner: "masteine", Name: "relay-sandbox"}, Reacts: map[int64][]Reaction{}, nextIssue: 1}
	for _, is := range issues {
		i := is
		f.Issues[i.Number] = &i
		if i.Number >= f.nextIssue {
			f.nextIssue = i.Number + 1
		}
		for _, c := range i.Comments {
			if c.ID >= f.nextID {
				f.nextID = c.ID + 1
			}
		}
	}
	return f
}

func (f *Fake) failing(method string) error {
	if f.Fail != nil {
		return f.Fail
	}
	return f.FailOn[method]
}

func (f *Fake) issue(number int) (*Issue, error) {
	is, ok := f.Issues[number]
	if !ok {
		return nil, fmt.Errorf("issue #%d: not found", number)
	}
	return is, nil
}

func (f *Fake) IsPrivate(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("IsPrivate"); err != nil {
		return false, err
	}
	return f.Private, nil
}

func (f *Fake) Issue(_ context.Context, number int) (Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("Issue"); err != nil {
		return Issue{}, err
	}
	is, err := f.issue(number)
	if err != nil {
		return Issue{}, err
	}
	out := *is
	out.Labels = append([]string(nil), is.Labels...)
	out.Comments = append([]Comment(nil), is.Comments...)
	return out, nil
}

func (f *Fake) CreateComment(_ context.Context, number int, body string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("CreateComment"); err != nil {
		return 0, err
	}
	is, err := f.issue(number)
	if err != nil {
		return 0, err
	}
	id := f.nextID
	f.nextID++
	is.Comments = append(is.Comments, Comment{ID: id, Author: f.CommentAs, Body: body, CreatedAt: time.Now()})
	f.Log = append(f.Log, fmt.Sprintf("comment #%d", number))
	return id, nil
}

func (f *Fake) EditIssue(_ context.Context, number int, title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("EditIssue"); err != nil {
		return err
	}
	is, err := f.issue(number)
	if err != nil {
		return err
	}
	if title != "" {
		is.Title = title
	}
	is.Body = body
	f.Log = append(f.Log, fmt.Sprintf("edit #%d", number))
	return nil
}

func (f *Fake) AddLabels(_ context.Context, number int, labels []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("AddLabels"); err != nil {
		return err
	}
	is, err := f.issue(number)
	if err != nil {
		return err
	}
	for _, l := range labels {
		if !contains(is.Labels, l) {
			is.Labels = append(is.Labels, l)
		}
		f.Log = append(f.Log, fmt.Sprintf("add #%d %s", number, l))
	}
	return nil
}

func (f *Fake) RemoveLabel(_ context.Context, number int, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("RemoveLabel"); err != nil {
		return err
	}
	is, err := f.issue(number)
	if err != nil {
		return err
	}
	var kept []string
	for _, l := range is.Labels {
		if l != label {
			kept = append(kept, l)
		}
	}
	is.Labels = kept
	f.Log = append(f.Log, fmt.Sprintf("remove #%d %s", number, label))
	return nil
}

func (f *Fake) FileContent(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("FileContent"); err != nil {
		return "", err
	}
	s, ok := f.Files[path]
	if !ok {
		return "", fmt.Errorf("%s: not found", path)
	}
	return s, nil
}

func (f *Fake) Login(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("Login"); err != nil {
		return "", err
	}
	return f.Me, nil
}

func (f *Fake) CreateIssue(_ context.Context, title, body string, labels []string) (Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("CreateIssue"); err != nil {
		return Issue{}, err
	}
	n := f.nextIssue
	f.nextIssue++
	is := &Issue{Number: n, Title: title, Body: body, Labels: append([]string(nil), labels...), URL: fmt.Sprintf("https://github.com/%s/issues/%d", f.Repo, n)}
	f.Issues[n] = is
	f.Log = append(f.Log, fmt.Sprintf("create #%d", n))
	return *is, nil
}

// ListUpdated returns every Issue; the fake does not track update times but
// records the window it was asked for in LastSince.
func (f *Fake) ListUpdated(_ context.Context, since time.Time) ([]Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.LastSince = since
	if err := f.failing("ListUpdated"); err != nil {
		return nil, err
	}
	var out []Issue
	for _, is := range f.Issues {
		i := *is
		i.Comments = nil
		i.Labels = append([]string(nil), is.Labels...)
		out = append(out, i)
	}
	return out, nil
}

func (f *Fake) Reactions(_ context.Context, commentID int64) ([]Reaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("Reactions"); err != nil {
		return nil, err
	}
	return append([]Reaction(nil), f.Reacts[commentID]...), nil
}

func (f *Fake) React(_ context.Context, commentID int64, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("React"); err != nil {
		return err
	}
	f.Reacts[commentID] = append(f.Reacts[commentID], Reaction{User: f.Me, Content: content})
	f.Log = append(f.Log, fmt.Sprintf("react %d %s", commentID, content))
	return nil
}

func (f *Fake) ActiveRuns(context.Context, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing("ActiveRuns"); err != nil {
		return 0, err
	}
	return f.Runs, nil
}

// LastComment returns the body of the newest comment on an Issue.
func (f *Fake) LastComment(number int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.Issues[number]
	if is == nil || len(is.Comments) == 0 {
		return ""
	}
	return is.Comments[len(is.Comments)-1].Body
}

// Comments returns how many comments an Issue has.
func (f *Fake) Comments(number int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if is := f.Issues[number]; is != nil {
		return len(is.Comments)
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
