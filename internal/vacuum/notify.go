package vacuum

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// WearThreshold is the remaining life, in percent, that triggers a part notice.
const WearThreshold = 10

// Notifier compares consecutive vacuum readings and sends a notice when the
// verified core says something changed: a job finished, a new error, or a
// part dropping below WearThreshold.
type Notifier struct {
	client *Client
	send   func(ctx context.Context, title, body string) error
	seen   bool
	status Status
	wear   map[string]int
}

func NewNotifier(client *Client, send func(ctx context.Context, title, body string) error) *Notifier {
	return &Notifier{client: client, send: send}
}

func words(name string) string { return strings.ReplaceAll(name, "_", " ") }

// Check takes one reading and sends any notices it calls for.
func (n *Notifier) Check(ctx context.Context) error {
	status, err := n.client.Status(ctx)
	if err != nil {
		return err
	}
	details, err := n.client.Details(ctx)
	if err != nil {
		return err
	}
	previous, previousWear, first := n.status, n.wear, !n.seen
	n.status, n.wear, n.seen = status, details.Consumables, true
	if first {
		return nil
	}
	name := status.Name
	if name == "" {
		name = "The vacuum"
	}
	var failures []error
	notify := func(title, body string) {
		if err := n.send(ctx, title, body); err != nil {
			failures = append(failures, err)
		}
	}
	if policy.VacuumNewError(previous.Error, status.Error) {
		where := ""
		if status.CurrentRoom != "" {
			where = " in the " + status.CurrentRoom
		}
		notify(name+" needs help", fmt.Sprintf("%s%s.", strings.ToUpper(words(status.Error)[:1])+words(status.Error)[1:], where))
	}
	parts := make([]string, 0, len(details.Consumables))
	for part := range details.Consumables {
		parts = append(parts, part)
	}
	sort.Strings(parts)
	for _, part := range parts {
		before, known := previousWear[part]
		if known && policy.VacuumPartWorn(before, details.Consumables[part], WearThreshold) {
			notify(fmt.Sprintf("%s: replace the %s soon", name, words(part)),
				fmt.Sprintf("%d%% left. Reset its counter after replacing it.", details.Consumables[part]))
		}
	}
	if policy.VacuumJobFinished(previous.State, status.State) {
		notify(name+" finished cleaning", fmt.Sprintf("%d m² in %d min.", status.CleanedAreaM2, status.CleaningMinutes))
	}
	return errors.Join(failures...)
}

// Run checks every interval until ctx ends; errors are logged by the caller's logf.
func (n *Notifier) Run(ctx context.Context, interval time.Duration, logf func(string, ...any)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := n.Check(ctx); err != nil && !errors.Is(err, ErrUnavailable) && ctx.Err() == nil {
			logf("vacuum notifications: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

var ntfyTopic = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// NewNtfySender posts notices to an ntfy topic. The topic name works like a
// password, so it must be long and random.
func NewNtfySender(base, topic string) (func(ctx context.Context, title, body string) error, error) {
	if !ntfyTopic.MatchString(topic) {
		return nil, errors.New("ntfy topic must be 16-64 letters, digits, _ or -")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, errors.New("ntfy URL must be http(s)://host")
	}
	target := strings.TrimRight(base, "/") + "/" + topic
	httpClient := &http.Client{Timeout: 15 * time.Second}
	return func(ctx context.Context, title, body string) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Title", title)
		response, err := httpClient.Do(request)
		if err != nil {
			return errors.New("could not reach the notification service")
		}
		defer response.Body.Close()
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode >= 300 {
			return fmt.Errorf("notification service answered %d", response.StatusCode)
		}
		return nil
	}, nil
}
