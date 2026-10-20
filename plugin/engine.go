// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Rule struct {
	Enabled   bool     `json:"enabled"`
	Reserve5h *float64 `json:"reserve_5h"`
	Reserve7d *float64 `json:"reserve_7d"`
}

type Observation struct {
	Remaining5h *float64  `json:"remaining_5h"`
	Remaining7d *float64  `json:"remaining_7d"`
	CheckedAt   time.Time `json:"checked_at"`
	Error       string    `json:"error,omitempty"`
}

type AuthFile struct {
	Name      string `json:"name"`
	AuthIndex string `json:"auth_index"`
	Provider  string `json:"provider"`
	Type      string `json:"type"`
	Disabled  bool   `json:"disabled"`
}

type Host interface {
	List() ([]AuthFile, error)
	Read(string) (map[string]json.RawMessage, error)
	Save(string, map[string]json.RawMessage) error
}

type Engine struct {
	mu           sync.RWMutex
	cycle        sync.Mutex
	rules        map[string]Rule
	observations map[string]Observation
	stateFile    string
	host         Host
	probe        func(context.Context, map[string]json.RawMessage) (Observation, error)
	cancel       context.CancelFunc
	done         chan struct{}
}

var quotaEndpoint = "https://chatgpt.com/backend-api/wham/usage"

func newEngine(stateFile string, host Host, proxyURL string) (*Engine, error) {
	e := &Engine{stateFile: stateFile, host: host, rules: map[string]Rule{}, observations: map[string]Observation{}}
	raw, err := os.ReadFile(stateFile)
	if err == nil {
		if err = json.Unmarshal(raw, &e.rules); err != nil {
			return nil, fmt.Errorf("read rules: %w", err)
		}
		if e.rules == nil {
			e.rules = map[string]Rule{}
		}
		for _, rule := range e.rules {
			if err = validateRule(rule); err != nil {
				return nil, err
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	e.probe = func(ctx context.Context, credential map[string]json.RawMessage) (Observation, error) {
		return fetchQuota(ctx, credential, proxyURL, quotaEndpoint)
	}
	return e, nil
}

func validateRule(rule Rule) error {
	for _, p := range []*float64{rule.Reserve5h, rule.Reserve7d} {
		if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 100) {
			return fmt.Errorf("percent must be between 0 and 100")
		}
	}
	return nil
}

func (e *Engine) get(name string) (Rule, Observation) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rules[name], e.observations[name]
}

func (e *Engine) set(ctx context.Context, name string, rule Rule) error {
	if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".json") {
		return fmt.Errorf("invalid credential filename")
	}
	if err := validateRule(rule); err != nil {
		return err
	}
	e.cycle.Lock()
	defer e.cycle.Unlock()
	files, err := e.host.List()
	if err != nil {
		return err
	}
	var file *AuthFile
	for i := range files {
		if files[i].Name == name && isCodex(files[i]) {
			file = &files[i]
			break
		}
	}
	if file == nil {
		return fmt.Errorf("Codex credential not found")
	}
	e.mu.Lock()
	next := make(map[string]Rule, len(e.rules)+1)
	for key, value := range e.rules {
		next[key] = value
	}
	next[name] = rule
	raw, err := json.MarshalIndent(next, "", "  ")
	if err == nil {
		err = atomicWrite(e.stateFile, raw)
	}
	if err == nil {
		e.rules = next
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	e.check(ctx, *file)
	return nil
}

func atomicWrite(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".limit-gpt-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func isCodex(file AuthFile) bool {
	return strings.EqualFold(file.Provider, "codex") || strings.EqualFold(file.Type, "codex")
}

func desiredState(rule Rule, q Observation) *bool {
	pairs := [][2]*float64{{rule.Reserve5h, q.Remaining5h}, {rule.Reserve7d, q.Remaining7d}}
	matched, equal := false, false
	for _, pair := range pairs {
		if pair[0] == nil || pair[1] == nil {
			continue
		}
		matched = true
		if *pair[1] < *pair[0] {
			disabled := true
			return &disabled
		}
		if *pair[1] == *pair[0] {
			equal = true
		}
	}
	if !matched || equal {
		return nil
	}
	disabled := false
	return &disabled
}

func (e *Engine) check(ctx context.Context, file AuthFile) {
	rule, _ := e.get(file.Name)
	if !rule.Enabled || (rule.Reserve5h == nil && rule.Reserve7d == nil) || !isCodex(file) {
		return
	}
	credential, err := e.host.Read(file.AuthIndex)
	var q Observation
	if err == nil {
		q, err = e.probe(ctx, credential)
	}
	q.CheckedAt = time.Now().UTC()
	if err == nil {
		if desired := desiredState(rule, q); desired != nil {
			// Re-read before writing so a token refresh is not overwritten by the probe's snapshot.
			credential, err = e.host.Read(file.AuthIndex)
			if err == nil {
				var disabled bool
				_ = json.Unmarshal(credential["disabled"], &disabled)
				if disabled != *desired || file.Disabled != *desired {
					credential["disabled"], _ = json.Marshal(*desired)
					err = e.host.Save(file.Name, credential)
				}
			}
		}
	}
	if err != nil {
		q.Error = err.Error()
	}
	e.mu.Lock()
	e.observations[file.Name] = q
	e.mu.Unlock()
}

func (e *Engine) runOnce(ctx context.Context) {
	e.cycle.Lock()
	defer e.cycle.Unlock()
	files, err := e.host.List()
	if err != nil {
		return
	}
	for _, file := range files {
		if ctx.Err() != nil {
			return
		}
		e.check(ctx, file)
	}
}

func (e *Engine) start(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel, e.done = cancel, make(chan struct{})
	go func() {
		defer close(e.done)
		e.runOnce(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.runOnce(ctx)
			}
		}
	}()
}

func (e *Engine) stop() {
	if e.cancel != nil {
		e.cancel()
		<-e.done
	}
}

func stringField(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func fetchQuota(ctx context.Context, credential map[string]json.RawMessage, proxyURL, endpoint string) (Observation, error) {
	token := stringField(credential, "access_token")
	if token == "" {
		return Observation{}, fmt.Errorf("credential has no access token")
	}
	if p := stringField(credential, "proxy_url"); p != "" {
		proxyURL = p
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return Observation{}, fmt.Errorf("invalid proxy URL")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Observation{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.134.0")
	accountID := stringField(credential, "account_id")
	if accountID == "" {
		accountID = stringField(credential, "chatgpt_account_id")
	}
	if accountID == "" {
		parts := strings.Split(stringField(credential, "id_token"), ".")
		if len(parts) == 3 {
			payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var claims struct {
				Auth struct {
					AccountID string `json:"chatgpt_account_id"`
				} `json:"https://api.openai.com/auth"`
			}
			if json.Unmarshal(payload, &claims) == nil {
				accountID = claims.Auth.AccountID
			}
		}
	}
	if accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Observation{}, fmt.Errorf("quota request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Observation{}, fmt.Errorf("quota request returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Observation{}, fmt.Errorf("read quota response failed")
	}
	return parseQuota(raw)
}

func parseQuota(raw []byte) (Observation, error) {
	type window struct {
		Used    *float64 `json:"used_percent"`
		Seconds int      `json:"limit_window_seconds"`
		Minutes int      `json:"window_minutes"`
	}
	var response struct {
		RateLimit *struct {
			Primary   *window `json:"primary_window"`
			Secondary *window `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return Observation{}, fmt.Errorf("invalid quota response")
	}
	if response.RateLimit == nil {
		return Observation{}, fmt.Errorf("quota response has no rate_limit")
	}
	var q Observation
	for _, w := range []*window{response.RateLimit.Primary, response.RateLimit.Secondary} {
		if w == nil {
			continue
		}
		if w.Used == nil || *w.Used < 0 || *w.Used > 100 {
			return Observation{}, fmt.Errorf("invalid quota percentage")
		}
		seconds := w.Seconds
		if seconds == 0 {
			seconds = w.Minutes * 60
		}
		remaining := 100 - *w.Used
		switch seconds {
		case 5 * 60 * 60:
			q.Remaining5h = &remaining
		case 7 * 24 * 60 * 60:
			q.Remaining7d = &remaining
		}
	}
	return q, nil
}
