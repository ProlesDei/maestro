package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// BotPattern is what a launcher must look like to have a webhook: its
// name becomes a file name.
var BotPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Webhooks live one per bot in notify/<bot>.url next to the store, outside
// every work tree, readable only by their owner: a webhook URL often holds
// its secret.
func webhookPath(dir, bot string) string { return filepath.Join(dir, bot+".url") }

// CheckURL refuses anything but an http or https URL with a host.
func CheckURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.ContainsAny(s, " \t\r\n") {
		return errors.New("want one http or https URL")
	}
	return nil
}

// SetWebhook saves bot's webhook URL, or removes it when url is "".
func SetWebhook(dir, bot, u string) error {
	if !BotPattern.MatchString(bot) {
		return fmt.Errorf("bot id %q: use lowercase letters, digits and single hyphens", bot)
	}
	if u == "" {
		err := os.Remove(webhookPath(dir, bot))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := CheckURL(u); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(u + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), webhookPath(dir, bot))
}

// Webhook returns bot's webhook URL, or "" if it has none.
func Webhook(dir, bot string) (string, error) {
	if !BotPattern.MatchString(bot) {
		return "", nil
	}
	b, err := os.ReadFile(webhookPath(dir, bot))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	u := strings.TrimSpace(string(b))
	if err := CheckURL(u); err != nil {
		return "", fmt.Errorf("webhook for %s: %v", bot, err)
	}
	return u, nil
}

// Webhooks lists the bots that have one.
func Webhooks(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var bots []string
	for _, e := range ents {
		if b, ok := strings.CutSuffix(e.Name(), ".url"); ok && BotPattern.MatchString(b) {
			bots = append(bots, b)
		}
	}
	return bots, err
}

// Host is a webhook URL with only its scheme and host: safe to print.
func Host(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "?"
	}
	return p.Scheme + "://" + p.Host + "/…"
}

// Notification is the body of a ring. It names the run and says how to
// read it; the result itself never leaves the machine.
type Notification struct {
	Text     string `json:"text"`
	Run      string `json:"run"`
	Ticket   string `json:"ticket,omitempty"`
	Launcher string `json:"launcher"`
	State    string `json:"state"`
}

// Notice is the ring for an ended run.
func Notice(r Record) Notification {
	on := ""
	if r.Ticket != "" {
		on = " on " + r.Ticket
	}
	return Notification{
		Text:     fmt.Sprintf("Maestro run %s%s ended: %s. Run maestro-ticket pickup to read its result.", r.ID, on, r.State),
		Run:      r.ID,
		Ticket:   r.Ticket,
		Launcher: r.Launcher,
		State:    r.State,
	}
}

// Ring POSTs n as JSON to u; any 2xx answer counts.
func Ring(ctx context.Context, u string, n Notification) error {
	b, _ := json.Marshal(n)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return errors.New("bad webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The error repeats the URL, and with it any secret it holds.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("ring %s: %v", Host(u), err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ring %s: %s", Host(u), resp.Status)
	}
	return nil
}
