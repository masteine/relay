// Package core is the messenger-independent part of the bot. It turns
// messages into Issues and comments, forwards Relay's stage comments back,
// turns button presses into commands, and notices when the runner is
// silent. State lives in GitHub: a 👀 reaction by the bot's own login marks
// a comment as forwarded, so a restart neither repeats nor loses anything.
//
// Everything runs on the Run goroutine; the mutex only covers the maps for
// callers that drive Handle and Poll from tests.
package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/masteine/relay/internal/bot/transport"
	"github.com/masteine/relay/internal/gh"
	"github.com/masteine/relay/internal/protocol"
)

// Clients makes a GitHub client for a repository; the core keeps one per
// alias.
type Clients func(repo gh.Repo) (gh.Client, error)

// deliverTimeout bounds a forward (send plus react) once it has started,
// even when the bot is being stopped: a message that went out must get its
// reaction, or it would be sent again after the restart.
const deliverTimeout = 20 * time.Second

// Core is the bot.
type Core struct {
	Config    Config
	Transport transport.Transport
	Clients   Clients
	// Now is the clock; tests replace it.
	Now func() time.Time
	// Log receives progress lines; nil means stderr.
	Log io.Writer

	mu      sync.Mutex
	clients map[string]gh.Client
	me      string
	// lastPoll is per repository and moves only after a fully successful
	// poll of that repository, so that an outage never skips a window.
	lastPoll map[string]time.Time
	// seen is the fast path for comments forwarded by this process; the
	// reaction on the comment is the durable record.
	seen map[string]bool
	// waiting records, per Issue, the id of the comment the owner's last
	// message produced, for the silence notice.
	waiting map[string]wait
}

type wait struct {
	since   time.Time
	comment int64
}

// New wires a Core.
func New(cfg Config, tr transport.Transport, clients Clients) *Core {
	return &Core{
		Config: cfg, Transport: tr, Clients: clients, Now: time.Now,
		clients: map[string]gh.Client{}, lastPoll: map[string]time.Time{}, seen: map[string]bool{}, waiting: map[string]wait{},
	}
}

func (c *Core) logf(format string, args ...any) {
	w := c.Log
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "relay bot: "+format+"\n", args...)
}

func (c *Core) client(repo gh.Repo) (gh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.clients[repo.String()]; ok {
		return cl, nil
	}
	cl, err := c.Clients(repo)
	if err != nil {
		return nil, err
	}
	c.clients[repo.String()] = cl
	return cl, nil
}

// Run receives messages and polls GitHub until ctx ends or the transport
// closes its channel. A poll or a message in flight finishes first.
func (c *Core) Run(ctx context.Context) error {
	in, err := c.Transport.Receive(ctx)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(c.Config.Poll)
	defer ticker.Stop()
	c.logf("started: %d alias(es), %d allowed chat(s), poll %s", len(c.Config.Aliases), len(c.Config.Chats()), c.Config.Poll)
	if err := c.Poll(ctx); err != nil {
		c.logf("poll: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-in:
			if !ok {
				return nil
			}
			if err := c.Handle(ctx, msg); err != nil {
				c.logf("handle: %v", err)
			}
		case <-ticker.C:
			if err := c.Poll(ctx); err != nil {
				c.logf("poll: %v", err)
			}
		}
	}
}

var (
	aliasRe = regexp.MustCompile(`(?s)^\s*([A-Za-z0-9_-]+)\s*:\s*(.+)$`)
	refRe   = regexp.MustCompile(`([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(\d+)`)
	cbRe    = regexp.MustCompile(`^([tar]):([a-z0-9_-]+):(\d+)$`)
)

// Handle processes one message or button press.
func (c *Core) Handle(ctx context.Context, in transport.Incoming) error {
	if !c.Config.Allowed(string(in.Chat), in.User) {
		c.logf("dropped a message from chat %s user %s: not allowed", in.Chat, in.User)
		return nil
	}
	if in.Callback != nil {
		return c.handleButton(ctx, in)
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil
	}
	// A reply to a forwarded comment goes to that Issue.
	if m := refRe.FindStringSubmatch(in.ReplyTo); m != nil {
		if repo, err := gh.ParseRepo(m[1]); err == nil {
			if alias, ok := c.Config.Alias(repo); ok {
				repo, _ = c.Config.Repo(alias) // the configured spelling
				if n, err := strconv.Atoi(m[2]); err == nil && n > 0 {
					return c.comment(ctx, in.Chat, repo, n, text)
				}
			}
		}
	}
	// "alias: text" opens an Issue.
	if m := aliasRe.FindStringSubmatch(text); m != nil {
		alias := strings.ToLower(m[1])
		repo, ok := c.Config.Repo(alias)
		if !ok {
			return c.hint(ctx, in, fmt.Sprintf("Не знаю аліаса «%s». Є: %s", m[1], strings.Join(c.Config.AliasList(), ", ")))
		}
		return c.createIssue(ctx, in.Chat, alias, repo, strings.TrimSpace(m[2]))
	}
	return c.hint(ctx, in, fmt.Sprintf("Напишіть «<аліас>: задача», щоб створити Issue, або відповідайте на повідомлення Relay. Аліаси: %s", strings.Join(c.Config.AliasList(), ", ")))
}

// hint answers an unrecognised message, in private chats only: in a group
// every stray message would get one.
func (c *Core) hint(ctx context.Context, in transport.Incoming, text string) error {
	if !in.Private {
		return nil
	}
	return c.send(ctx, in.Chat, text)
}

func (c *Core) handleButton(ctx context.Context, in transport.Incoming) error {
	m := cbRe.FindStringSubmatch(strings.ToLower(in.Callback.Data))
	if m == nil {
		return c.Transport.Ack(ctx, *in.Callback, "Кнопка застаріла")
	}
	cmd := map[string]protocol.Command{"t": protocol.CmdTicket, "a": protocol.CmdApprove, "r": protocol.CmdRetry}[m[1]]
	repo, ok := c.Config.Repo(m[2])
	if !ok {
		return c.Transport.Ack(ctx, *in.Callback, "Невідомий аліас")
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 {
		return c.Transport.Ack(ctx, *in.Callback, "Кнопка застаріла")
	}
	if err := c.post(ctx, repo, n, "/"+string(cmd)); err != nil {
		_ = c.Transport.Ack(ctx, *in.Callback, "Не вдалося: "+shorten(err.Error(), 100))
		return err
	}
	return c.Transport.Ack(ctx, *in.Callback, fmt.Sprintf("/%s → %s#%d", cmd, repo, n))
}

// createIssue is the bot's "todo: text": a draft Issue with the original
// text as the first comment, so that /ticket can rewrite the body later.
func (c *Core) createIssue(ctx context.Context, chat transport.ChatID, alias string, repo gh.Repo, text string) error {
	cl, err := c.client(repo)
	if err != nil {
		return err
	}
	title := firstLine(text)
	if r := []rune(title); len(r) > 80 {
		title = string(r[:77]) + "…"
	}
	is, err := cl.CreateIssue(ctx, title, "Чернетка: іде обговорення. Формулювання з'явиться тут після `/ticket`.", []string{string(protocol.StateDiscuss)})
	if err != nil {
		return c.send(ctx, chat, "Не вдалося створити Issue: "+shorten(err.Error(), 200))
	}
	id, err := cl.CreateComment(ctx, is.Number, text)
	if err != nil {
		return c.send(ctx, chat, fmt.Sprintf("Issue %s#%d створено, але перший коментар не записався: %s", repo, is.Number, shorten(err.Error(), 200)))
	}
	c.markWaiting(repo, is.Number, id)
	c.logf("%s#%d created", repo, is.Number)
	return c.send(ctx, chat, fmt.Sprintf("✅ %s · %s#%d · %s\n%s", alias, repo, is.Number, title, is.URL))
}

// comment posts the owner's text to an Issue and confirms briefly.
func (c *Core) comment(ctx context.Context, chat transport.ChatID, repo gh.Repo, n int, text string) error {
	if err := c.post(ctx, repo, n, text); err != nil {
		return c.send(ctx, chat, fmt.Sprintf("Не вдалося написати в %s#%d: %s", repo, n, shorten(err.Error(), 200)))
	}
	return c.send(ctx, chat, fmt.Sprintf("📨 %s#%d", repo, n))
}

func (c *Core) post(ctx context.Context, repo gh.Repo, n int, text string) error {
	cl, err := c.client(repo)
	if err != nil {
		return err
	}
	id, err := cl.CreateComment(ctx, n, text)
	if err != nil {
		return err
	}
	c.markWaiting(repo, n, id)
	c.logf("%s#%d: posted comment %d", repo, n, id)
	return nil
}

func (c *Core) send(ctx context.Context, chat transport.ChatID, text string) error {
	_, err := c.Transport.Send(ctx, chat, transport.Outgoing{Text: text})
	return err
}

func (c *Core) markWaiting(repo gh.Repo, n int, comment int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waiting[key(repo, n)] = wait{since: c.Now(), comment: comment}
}

func key(repo gh.Repo, n int) string { return fmt.Sprintf("%s#%d", repo, n) }

// Poll forwards new Relay comments and runs the silence check, for every
// repository, carrying on past failures and returning the first one.
func (c *Core) Poll(ctx context.Context) error {
	var errs []error
	for _, alias := range c.Config.AliasList() {
		repo, _ := c.Config.Repo(alias)
		if err := c.pollRepo(ctx, alias, repo); err != nil {
			c.logf("%s: %v", repo, err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Core) pollRepo(ctx context.Context, alias string, repo gh.Repo) error {
	cl, err := c.client(repo)
	if err != nil {
		return err
	}
	if c.me == "" {
		me, err := cl.Login(ctx)
		if err != nil {
			return err
		}
		c.me = me
	}
	now := c.Now()
	c.mu.Lock()
	last := c.lastPoll[repo.String()]
	c.mu.Unlock()
	// The window starts one minute before the last successful poll
	// (overlap: a comment is never missed, and the reaction makes a repeat
	// harmless) and never earlier than Lookback, so that a failure that
	// keeps recurring cannot make every poll list the whole history.
	floor := now.Add(-c.Config.Lookback)
	since := floor
	if !last.IsZero() && last.Add(-time.Minute).After(floor) {
		since = last.Add(-time.Minute)
	}
	issues, err := cl.ListUpdated(ctx, since)
	if err != nil {
		return err
	}
	var errs []error
	for _, brief := range issues {
		if !hasRelayLabel(brief.Labels) {
			continue
		}
		issue, err := cl.Issue(ctx, brief.Number)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := c.forward(ctx, cl, alias, repo, issue); err != nil {
			errs = append(errs, err)
		}
	}
	// The silence check does not depend on the window, so its failures
	// are reported but do not hold the window back.
	silenceErr := c.silence(ctx, cl, alias, repo)
	if len(errs) > 0 {
		return errors.Join(append(errs, silenceErr)...)
	}
	c.mu.Lock()
	c.lastPoll[repo.String()] = now
	c.mu.Unlock()
	return silenceErr
}

func hasRelayLabel(labels []string) bool {
	for _, l := range labels {
		if l == protocol.LabelPipeline || protocol.IsState(l) {
			return true
		}
	}
	return false
}

// forward sends every Relay comment the bot has not reacted to yet, oldest
// first, and reacts after each send. One chat refusing a message does not
// stop the others; the reaction goes on once at least one chat has it
// (at-least-once: a crash between the two may repeat one message, never
// lose one). A comment that no chat accepted stops this Issue, so that
// order is kept, and is tried again next poll.
func (c *Core) forward(ctx context.Context, cl gh.Client, alias string, repo gh.Repo, issue gh.Issue) error {
	labels, _ := protocol.ParseLabels(issue.Labels)
	for _, cm := range issue.Protocol().Comments {
		if cm.Author != protocol.BotLogin || !protocol.IsRelayComment(cm.Body) {
			continue
		}
		k := fmt.Sprintf("%s#%d/%d", repo, issue.Number, cm.ID)
		c.mu.Lock()
		seen := c.seen[k]
		c.mu.Unlock()
		if seen {
			continue
		}
		reacted, err := c.reacted(ctx, cl, cm.ID)
		if err != nil {
			return err
		}
		if reacted {
			c.markSeen(k, repo, issue.Number, cm.ID)
			continue
		}
		// Once a forward starts it completes even if the bot is stopping.
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverTimeout)
		msg := Format(alias, repo, issue, cm, labels)
		delivered := 0
		var sendErrs []error
		for _, chat := range c.Config.Chats() {
			if _, err := c.Transport.Send(dctx, transport.ChatID(chat), msg); err != nil {
				sendErrs = append(sendErrs, fmt.Errorf("chat %s: %w", chat, err))
				continue
			}
			delivered++
		}
		if delivered == 0 {
			cancel()
			return fmt.Errorf("%s#%d comment %d: no chat accepted it: %w", repo, issue.Number, cm.ID, errors.Join(sendErrs...))
		}
		for _, e := range sendErrs {
			c.logf("%s#%d comment %d: %v", repo, issue.Number, cm.ID, e)
		}
		err = cl.React(dctx, cm.ID, gh.ReactionEyes)
		cancel()
		if err != nil {
			return err
		}
		c.markSeen(k, repo, issue.Number, cm.ID)
		c.logf("%s#%d: forwarded comment %d to %d chat(s)", repo, issue.Number, cm.ID, delivered)
	}
	return nil
}

// markSeen records a forwarded comment and clears a silence wait that the
// comment answers (only a comment newer than the owner's message counts).
func (c *Core) markSeen(k string, repo gh.Repo, n int, commentID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[k] = true
	if w, ok := c.waiting[key(repo, n)]; ok && commentID > w.comment {
		delete(c.waiting, key(repo, n))
	}
}

func (c *Core) reacted(ctx context.Context, cl gh.Client, commentID int64) (bool, error) {
	rs, err := cl.Reactions(ctx, commentID)
	if err != nil {
		return false, err
	}
	for _, r := range rs {
		if r.Content == gh.ReactionEyes && strings.EqualFold(r.User, c.me) {
			return true, nil
		}
	}
	return false, nil
}

// silence tells the owner when nothing happened for Config.Silence after
// they wrote: no run in progress, no Relay comment after their message.
// Once per wait.
func (c *Core) silence(ctx context.Context, cl gh.Client, alias string, repo gh.Repo) error {
	now := c.Now()
	var stale []string
	c.mu.Lock()
	for k, w := range c.waiting {
		if strings.HasPrefix(k, repo.String()+"#") && now.Sub(w.since) >= c.Config.Silence {
			stale = append(stale, k)
		}
	}
	c.mu.Unlock()
	if len(stale) == 0 {
		return nil
	}
	// A failing run check (the workflow is not there yet, a wrong file
	// name) is itself a reason to tell the owner, not to wait forever.
	runs, runErr := cl.ActiveRuns(ctx, c.Config.Workflow)
	if runErr != nil {
		runs = 0
	}
	var notices []string
	c.mu.Lock()
	for _, k := range stale {
		w, ok := c.waiting[k]
		if !ok {
			continue
		}
		if runs > 0 {
			w.since = now // a run exists; look again later
			c.waiting[k] = w
			continue
		}
		delete(c.waiting, k)
		notices = append(notices, k)
	}
	c.mu.Unlock()
	for _, k := range notices {
		text := fmt.Sprintf("⏳ %s · %s: минуло %s без run чи відповіді Relay. Mac спить або раннер офлайн?", alias, k, c.Config.Silence.Round(time.Minute))
		if runErr != nil {
			text += fmt.Sprintf("\nПеревірити runs не вдалося (%s): можливо, в репо ще немає workflow — запустіть /relay:init.", shorten(runErr.Error(), 120))
		}
		for _, chat := range c.Config.Chats() {
			if _, err := c.Transport.Send(ctx, transport.ChatID(chat), transport.Outgoing{Text: text}); err != nil {
				c.logf("%s: silence notice to chat %s: %v", k, chat, err)
			}
		}
		c.logf("%s: silence notice sent", k)
	}
	return runErr
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func shorten(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
