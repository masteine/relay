package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"
)

// API is the Client backed by the GitHub REST API.
type API struct {
	repo Repo
	gh   *github.Client
}

// New returns a Client for repo authenticated with token.
func New(repo Repo, token string) (*API, error) {
	c, err := github.NewClient(github.WithAuthToken(token))
	if err != nil {
		return nil, fmt.Errorf("github client: %w", err)
	}
	return &API{repo: repo, gh: c}, nil
}

// Token finds a GitHub token: GITHUB_TOKEN or GH_TOKEN in the environment
// (CI), otherwise `gh auth token` (the owner's Mac). The bot and the stages
// never store one themselves.
func Token() (string, error) {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("gh auth token: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("no GITHUB_TOKEN and gh is not available: %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", fmt.Errorf("gh auth token returned nothing; run gh auth login")
	}
	return tok, nil
}

func (a *API) IsPrivate(ctx context.Context) (bool, error) {
	r, _, err := a.gh.Repositories.Get(ctx, a.repo.Owner, a.repo.Name)
	if err != nil {
		return false, fmt.Errorf("get repository %s: %w", a.repo, err)
	}
	return r.GetPrivate(), nil
}

func (a *API) Issue(ctx context.Context, number int) (Issue, error) {
	is, _, err := a.gh.Issues.Get(ctx, a.repo.Owner, a.repo.Name, number)
	if err != nil {
		return Issue{}, fmt.Errorf("get issue #%d: %w", number, err)
	}
	out := Issue{
		Number:        is.GetNumber(),
		Title:         is.GetTitle(),
		Body:          is.GetBody(),
		URL:           is.GetHTMLURL(),
		IsPullRequest: is.IsPullRequest(),
	}
	for _, l := range is.Labels {
		out.Labels = append(out.Labels, l.GetName())
	}
	opts := &github.IssueListCommentsOptions{
		Sort:        github.Ptr("created"),
		Direction:   github.Ptr("asc"),
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		page, resp, err := a.gh.Issues.ListComments(ctx, a.repo.Owner, a.repo.Name, number, opts)
		if err != nil {
			return Issue{}, fmt.Errorf("list comments of #%d: %w", number, err)
		}
		for _, c := range page {
			out.Comments = append(out.Comments, Comment{
				ID:        c.GetID(),
				Author:    c.GetUser().GetLogin(),
				Body:      c.GetBody(),
				CreatedAt: c.GetCreatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}
	return out, nil
}

func (a *API) CreateComment(ctx context.Context, number int, body string) (int64, error) {
	c, _, err := a.gh.Issues.CreateComment(ctx, a.repo.Owner, a.repo.Name, number, github.IssueCommentRequest{Body: body})
	if err != nil {
		return 0, fmt.Errorf("comment on #%d: %w", number, err)
	}
	return c.GetID(), nil
}

func (a *API) EditIssue(ctx context.Context, number int, title, body string) error {
	req := github.UpdateIssueRequest{Body: github.Ptr(body)}
	if title != "" {
		req.Title = github.Ptr(title)
	}
	if _, _, err := a.gh.Issues.Update(ctx, a.repo.Owner, a.repo.Name, number, req); err != nil {
		return fmt.Errorf("edit #%d: %w", number, err)
	}
	return nil
}

func (a *API) AddLabels(ctx context.Context, number int, labels []string) error {
	if _, _, err := a.gh.Issues.AddLabelsToIssue(ctx, a.repo.Owner, a.repo.Name, number, labels); err != nil {
		return fmt.Errorf("add labels to #%d: %w", number, err)
	}
	return nil
}

func (a *API) FileContent(ctx context.Context, path string) (string, error) {
	f, _, _, err := a.gh.Repositories.GetContents(ctx, a.repo.Owner, a.repo.Name, path, nil)
	if err != nil {
		return "", fmt.Errorf("read %s from %s: %w", path, a.repo, err)
	}
	if f == nil {
		return "", fmt.Errorf("read %s from %s: not a file", path, a.repo)
	}
	s, err := f.GetContent()
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", path, err)
	}
	return s, nil
}

func (a *API) RemoveLabel(ctx context.Context, number int, label string) error {
	resp, err := a.gh.Issues.RemoveLabelForIssue(ctx, a.repo.Owner, a.repo.Name, number, label)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("remove label %s from #%d: %w", label, number, err)
	}
	return nil
}

func (a *API) Login(ctx context.Context) (string, error) {
	u, _, err := a.gh.Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("get authenticated user: %w", err)
	}
	return u.GetLogin(), nil
}

func (a *API) CreateIssue(ctx context.Context, title, body string, labels []string) (Issue, error) {
	is, _, err := a.gh.Issues.Create(ctx, a.repo.Owner, a.repo.Name, github.CreateIssueRequest{Title: title, Body: github.Ptr(body), Labels: labels})
	if err != nil {
		return Issue{}, fmt.Errorf("create issue in %s: %w", a.repo, err)
	}
	out := Issue{Number: is.GetNumber(), Title: is.GetTitle(), Body: is.GetBody(), URL: is.GetHTMLURL()}
	for _, l := range is.Labels {
		out.Labels = append(out.Labels, l.GetName())
	}
	return out, nil
}

func (a *API) ListUpdated(ctx context.Context, since time.Time) ([]Issue, error) {
	opts := &github.IssueListByRepoOptions{State: "all", Sort: "updated", Direction: "desc", Since: since, ListOptions: github.ListOptions{PerPage: 100}}
	var out []Issue
	// Pull requests count toward the page before they are filtered out, so
	// page until the end. The cap (2000 Issues updated inside the window)
	// is far beyond a personal repository; past it the oldest are skipped.
	for page := 0; page < 20; page++ {
		list, resp, err := a.gh.Issues.ListByRepo(ctx, a.repo.Owner, a.repo.Name, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues of %s: %w", a.repo, err)
		}
		for _, is := range list {
			if is.IsPullRequest() {
				continue
			}
			i := Issue{Number: is.GetNumber(), Title: is.GetTitle(), Body: is.GetBody(), URL: is.GetHTMLURL()}
			for _, l := range is.Labels {
				i.Labels = append(i.Labels, l.GetName())
			}
			out = append(out, i)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}
	return out, nil
}

func (a *API) Reactions(ctx context.Context, commentID int64) ([]Reaction, error) {
	list, _, err := a.gh.Reactions.ListIssueCommentReactions(ctx, a.repo.Owner, a.repo.Name, commentID, &github.ListReactionOptions{ListOptions: github.ListOptions{PerPage: 100}})
	if err != nil {
		return nil, fmt.Errorf("reactions of comment %d: %w", commentID, err)
	}
	var out []Reaction
	for _, r := range list {
		out = append(out, Reaction{User: r.GetUser().GetLogin(), Content: r.GetContent()})
	}
	return out, nil
}

func (a *API) React(ctx context.Context, commentID int64, content string) error {
	if _, _, err := a.gh.Reactions.CreateIssueCommentReaction(ctx, a.repo.Owner, a.repo.Name, commentID, content); err != nil {
		return fmt.Errorf("react to comment %d: %w", commentID, err)
	}
	return nil
}

func (a *API) ActiveRuns(ctx context.Context, workflowFile string) (int, error) {
	n := 0
	for _, status := range []string{"queued", "in_progress"} {
		runs, _, err := a.gh.Actions.ListWorkflowRunsByFileName(ctx, a.repo.Owner, a.repo.Name, workflowFile, &github.ListWorkflowRunsOptions{Status: status, ListOptions: github.ListOptions{PerPage: 1}})
		if err != nil {
			return 0, fmt.Errorf("list %s runs of %s: %w", status, a.repo, err)
		}
		n += runs.GetTotalCount()
	}
	return n, nil
}
