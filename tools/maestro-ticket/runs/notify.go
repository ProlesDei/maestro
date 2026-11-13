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
// its secret. A webhook that wants a bearer key keeps it in <bot>.key.
func webhookPath(dir, bot string) string { return filepath.Join(dir, bot+".url") }
func keyPath(dir, bot string) string     { return filepath.Join(dir, bot+".key") }

// Hook is a bot's webhook: its URL and, if it wants one, the key sent as
// Authorization: Bearer. Neither is ever printed or logged.
type Hook struct {
	URL, Key string
}

// CheckURL refuses anything but an http or https URL with a host.
func CheckURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.ContainsAny(s, " \t\r\n") {
		return errors.New("want one http or https URL")
	}
	return nil
}

// SetWebhook saves bot's webhook URL, or removes it, and its key, when u
// is "".
func SetWebhook(dir, bot, u string) error {
	if u == "" {
		if err := remove(webhookPath(dir, bot)); err != nil {
			return err
		}
		return remove(keyPath(dir, bot))
	}
	if err := CheckURL(u); err != nil {
		return err
	}
	return writeSecret(dir, bot, webhookPath(dir, bot), u)
}

// SetKey saves the bearer key bot's webhook wants, or removes it when key
// is "".
func SetKey(dir, bot, key string) error {
	if key == "" {
		return remove(keyPath(dir, bot))
	}
	if strings.ContainsAny(key, " \t\r\n") || len(key) > 4096 {
		return errors.New("want a key of one word, up to 4096 characters")
	}
	return writeSecret(dir, bot, keyPath(dir, bot), key)
}

func remove(name string) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// writeSecret writes a file only its owner can read, whole, through a
// rename.
func writeSecret(dir, bot, name, v string) error {
	if !BotPattern.MatchString(bot) {
		return fmt.Errorf("bot id %q: use lowercase letters, digits and single hyphens", bot)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(v + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

// Webhook returns bot's webhook, or a zero Hook if it has none.
func Webhook(dir, bot string) (Hook, error) {
	if !BotPattern.MatchString(bot) {
		return Hook{}, nil
	}
	b, err := os.ReadFile(webhookPath(dir, bot))
	if errors.Is(err, fs.ErrNotExist) {
		return Hook{}, nil
	}
	if err != nil {
		return Hook{}, err
	}
	h := Hook{URL: strings.TrimSpace(string(b))}
	if err := CheckURL(h.URL); err != nil {
		return Hook{}, fmt.Errorf("webhook for %s: %v", bot, err)
	}
	k, err := os.ReadFile(keyPath(dir, bot))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Hook{}, err
	}
	h.Key = strings.TrimSpace(string(k))
	return h, nil
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
		Text:     fmt.Sprintf("Maestro run %s%s ended: %s. Run maestro-ticket pickup to read it, then ack it.", r.ID, on, r.State),
		Run:      r.ID,
		Ticket:   r.Ticket,
		Launcher: r.Launcher,
		State:    r.State,
	}
}

// Ring POSTs n as JSON to h, with its key as a bearer token if it has
// one; any 2xx answer counts.
func Ring(ctx context.Context, h Hook, n Notification) error {
	u := h.URL
	b, _ := json.Marshal(n)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return errors.New("bad webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")
	if h.Key != "" {
		req.Header.Set("Authorization", "Bearer "+h.Key)
	}
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
