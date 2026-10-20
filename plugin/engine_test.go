// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func percent(n float64) *float64 { return &n }

func TestThresholds(t *testing.T) {
	tests := []struct {
		name  string
		rule  Rule
		quota Observation
		want  *bool
	}{
		{"low short", Rule{Reserve5h: percent(20), Reserve7d: percent(30)}, Observation{Remaining5h: percent(19), Remaining7d: percent(90)}, boolPtr(true)},
		{"low week", Rule{Reserve5h: percent(20), Reserve7d: percent(30)}, Observation{Remaining5h: percent(90), Remaining7d: percent(29)}, boolPtr(true)},
		{"both high", Rule{Reserve5h: percent(20), Reserve7d: percent(30)}, Observation{Remaining5h: percent(90), Remaining7d: percent(90)}, boolPtr(false)},
		{"equal", Rule{Reserve5h: percent(20)}, Observation{Remaining5h: percent(20)}, nil},
		{"low beats equal", Rule{Reserve5h: percent(20), Reserve7d: percent(30)}, Observation{Remaining5h: percent(20), Remaining7d: percent(10)}, boolPtr(true)},
		{"unset", Rule{}, Observation{Remaining5h: percent(1)}, nil},
		{"missing short", Rule{Reserve5h: percent(20), Reserve7d: percent(30)}, Observation{Remaining7d: percent(90)}, boolPtr(false)},
		{"no matching window", Rule{Reserve5h: percent(20)}, Observation{Remaining7d: percent(90)}, nil},
		{"zero equal", Rule{Reserve5h: percent(0)}, Observation{Remaining5h: percent(0)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desiredState(tt.rule, tt.quota)
			if (got == nil) != (tt.want == nil) || got != nil && *got != *tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
func boolPtr(v bool) *bool { return &v }

type fakeHost struct {
	mu       sync.Mutex
	disabled bool
	saves    int
	reads    int
	token    string
}

func (h *fakeHost) List() ([]AuthFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return []AuthFile{{Name: "account.json", AuthIndex: "index", Provider: "codex", Disabled: h.disabled}}, nil
}
func (h *fakeHost) Read(string) (map[string]json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reads++
	raw, _ := json.Marshal(map[string]any{"access_token": h.token, "disabled": h.disabled, "note": "keep me"})
	var out map[string]json.RawMessage
	_ = json.Unmarshal(raw, &out)
	return out, nil
}
func (h *fakeHost) Save(_ string, raw map[string]json.RawMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if stringField(raw, "note") != "keep me" || stringField(raw, "access_token") != h.token {
		return fmt.Errorf("credential contents changed")
	}
	_ = json.Unmarshal(raw["disabled"], &h.disabled)
	h.saves++
	return nil
}

func TestSaveDisableRecoverPersistAndTurnOff(t *testing.T) {
	ctx := context.Background()
	h := &fakeHost{token: "original"}
	path := filepath.Join(t.TempDir(), "rules.json")
	e, err := newEngine(path, h, "")
	if err != nil {
		t.Fatal(err)
	}
	remaining := 10.0
	failed := false
	e.probe = func(context.Context, map[string]json.RawMessage) (Observation, error) {
		if failed {
			return Observation{}, fmt.Errorf("offline")
		}
		return Observation{Remaining5h: &remaining}, nil
	}
	rule := Rule{Enabled: true, Reserve5h: percent(20)}
	if err := e.set(ctx, "account.json", rule); err != nil {
		t.Fatal(err)
	}
	if !h.disabled || h.saves != 1 {
		t.Fatal("save must immediately disable the credential")
	}
	e.runOnce(ctx)
	if h.saves != 1 {
		t.Fatal("unchanged state must not rewrite the credential")
	}
	failed = true
	remaining = 90
	e.runOnce(ctx)
	if !h.disabled {
		t.Fatal("query failure must preserve disabled status")
	}
	_, observation := e.get("account.json")
	if observation.Error == "" {
		t.Fatal("query failure must be visible")
	}
	failed = false
	e.runOnce(ctx)
	if h.disabled || h.saves != 2 {
		t.Fatal("disabled credential must still be checked and recover")
	}
	restarted, err := newEngine(path, h, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := restarted.get("account.json")
	if !got.Enabled || got.Reserve5h == nil || *got.Reserve5h != 20 {
		t.Fatal("settings were not persisted")
	}
	rule.Enabled = false
	if err := e.set(ctx, "account.json", rule); err != nil {
		t.Fatal(err)
	}
	remaining = 1
	e.runOnce(ctx)
	if h.disabled {
		t.Fatal("disabled rule must leave manual status alone")
	}
}

func TestProbeDoesNotOverwriteRefreshedCredential(t *testing.T) {
	h := &fakeHost{token: "old"}
	e, _ := newEngine(filepath.Join(t.TempDir(), "rules.json"), h, "")
	e.probe = func(context.Context, map[string]json.RawMessage) (Observation, error) {
		h.token = "new"
		return Observation{Remaining5h: percent(1)}, nil
	}
	if err := e.set(context.Background(), "account.json", Rule{Enabled: true, Reserve5h: percent(20)}); err != nil {
		t.Fatal(err)
	}
	if !h.disabled || h.token != "new" {
		t.Fatal("lost token refresh")
	}
}

func TestBackgroundCheckWithoutManagementPage(t *testing.T) {
	h := &fakeHost{token: "token"}
	e, _ := newEngine(filepath.Join(t.TempDir(), "rules.json"), h, "")
	e.rules["account.json"] = Rule{Enabled: true, Reserve5h: percent(20)}
	checked := make(chan struct{}, 1)
	e.probe = func(context.Context, map[string]json.RawMessage) (Observation, error) {
		checked <- struct{}{}
		return Observation{Remaining5h: percent(5)}, nil
	}
	e.start(time.Hour)
	select {
	case <-checked:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never queried quota")
	}
	e.stop()
	if !h.disabled {
		t.Fatal("background check did not disable credential")
	}
}

func TestQuotaParsingByWindowDuration(t *testing.T) {
	q, err := parseQuota([]byte(`{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":604800},"secondary_window":{"used_percent":40,"limit_window_seconds":18000}}}`))
	if err != nil || q.Remaining5h == nil || *q.Remaining5h != 60 || *q.Remaining7d != 88 {
		t.Fatalf("wrong windows: %+v %v", q, err)
	}
	q, err = parseQuota([]byte(`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":604800},"secondary_window":null}}`))
	if err != nil || q.Remaining5h != nil || *q.Remaining7d != 90 {
		t.Fatalf("weekly-only account: %+v %v", q, err)
	}
	for _, raw := range []string{`{}`, `invalid`, `{"rate_limit":{"primary_window":{"used_percent":101}}}`, `{"rate_limit":{"primary_window":{"limit_window_seconds":18000}}}`} {
		if _, err := parseQuota([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid quota: %s", raw)
		}
	}
}

func TestQuotaHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("Chatgpt-Account-Id") != "account" {
			t.Error("missing credential headers")
		}
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":70,"limit_window_seconds":18000}}}`))
	}))
	defer server.Close()
	credential := map[string]json.RawMessage{"access_token": json.RawMessage(`"test"`), "account_id": json.RawMessage(`"account"`)}
	q, err := fetchQuota(context.Background(), credential, "", server.URL)
	if err != nil || *q.Remaining5h != 30 {
		t.Fatalf("probe failed: %+v %v", q, err)
	}
}

func TestNativeRegistration(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "rules.json")
	config, _ := json.Marshal(struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{
		ConfigYAML: []byte(fmt.Sprintf("enabled: false\nstate_file: %q\n", stateFile)),
	})
	defer cliproxyPluginShutdown()
	for _, method := range []string{"plugin.register", "plugin.reconfigure"} {
		result, err := dispatch(method, config)
		if err != nil {
			t.Fatal(err)
		}
		registration := result.(map[string]any)
		metadata := registration["metadata"].(map[string]any)
		for _, field := range []string{"Name", "Version", "Author", "GitHubRepository"} {
			if metadata[field] == nil || metadata[field] == "" {
				t.Fatalf("missing host-required metadata: %s", field)
			}
		}
		if activeEngine.stateFile != stateFile {
			t.Fatal("plugin did not read its configuration")
		}
		if activeEngine.done != nil {
			t.Fatal("disabled plugin started a worker")
		}
	}
}
