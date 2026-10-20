// SPDX-License-Identifier: GPL-3.0-only

package main

/*
#include <stdint.h>
#include <stdlib.h>
typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; void* call; void* free_buffer; } cliproxy_host_api;
typedef int (*plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*plugin_free_fn)(void*, size_t);
typedef void (*plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; plugin_call_fn call; plugin_free_fn free_buffer; plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static const cliproxy_host_api* stored_host;
static void store_host(const cliproxy_host_api* host) { stored_host = host; }
static int call_host(const char* method, const uint8_t* request, size_t len, cliproxy_buffer* response) {
    if (!stored_host || !stored_host->call) return 1;
    return ((host_call_fn)stored_host->call)(stored_host->host_ctx, method, request, len, response);
}
static void free_host(void* ptr, size_t len) {
    if (stored_host && stored_host->free_buffer && ptr) ((host_free_fn)stored_host->free_buffer)(ptr, len);
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unsafe"

	"gopkg.in/yaml.v3"
)

var pluginMu sync.Mutex
var activeEngine *Engine

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil {
		return 1
	}
	C.store_host(host)
	plugin.abi_version = 1
	plugin.call = C.plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, length C.size_t, response *C.cliproxy_buffer) C.int {
	if response == nil {
		return 1
	}
	response.ptr, response.len = nil, 0
	if method == nil {
		return 1
	}
	var raw []byte
	if request != nil && length > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(length))
	}
	result, err := dispatch(C.GoString(method), raw)
	var data []byte
	if err != nil {
		data, _ = json.Marshal(envelope{Error: &rpcError{Code: "limit_gpt_error", Message: err.Error()}})
	} else {
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil {
			return 1
		}
		data, _ = json.Marshal(envelope{OK: true, Result: encoded})
	}
	response.ptr = C.CBytes(data)
	response.len = C.size_t(len(data))
	if err != nil {
		return 1
	}
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	if activeEngine != nil {
		activeEngine.stop()
		activeEngine = nil
	}
}

func dispatch(method string, raw []byte) (any, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		if err := configure(raw); err != nil {
			return nil, err
		}
		return map[string]any{
			"schema_version": 6,
			"metadata": map[string]any{"Name": "limit-gpt", "Version": "0.1.0", "Author": "Limit-gpt", "GitHubRepository": "https://github.com/Oldcucumber/Limit-gpt", "ConfigFields": []any{
				map[string]string{"Name": "state_file", "Type": "string", "Description": "规则保存位置，默认 plugins/limit-gpt.json"},
				map[string]string{"Name": "interval_seconds", "Type": "number", "Description": "检查间隔（秒），默认 60"},
				map[string]string{"Name": "proxy_url", "Type": "string", "Description": "额度查询代理，可留空；账号代理优先"},
			}},
			"capabilities": map[string]bool{"management_api": true},
		}, nil
	case "management.register":
		return map[string]any{"Routes": []map[string]string{
			{"Method": "GET", "Path": "/plugins/limit-gpt/rule"},
			{"Method": "PUT", "Path": "/plugins/limit-gpt/rule"},
		}}, nil
	case "management.handle":
		return handleManagement(raw)
	default:
		return nil, fmt.Errorf("unknown method %s", method)
	}
}

func configure(raw []byte) error {
	var lifecycle struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if err := json.Unmarshal(raw, &lifecycle); err != nil {
		return err
	}
	cfg := struct {
		StateFile       string `yaml:"state_file"`
		IntervalSeconds int    `yaml:"interval_seconds"`
		ProxyURL        string `yaml:"proxy_url"`
		Enabled         *bool  `yaml:"enabled"`
	}{StateFile: "plugins/limit-gpt.json", IntervalSeconds: 60}
	if err := yaml.Unmarshal(lifecycle.ConfigYAML, &cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.StateFile) == "" {
		cfg.StateFile = "plugins/limit-gpt.json"
	}
	if cfg.IntervalSeconds < 1 {
		cfg.IntervalSeconds = 60
	}
	pluginMu.Lock()
	defer pluginMu.Unlock()
	if activeEngine != nil {
		activeEngine.stop()
		activeEngine = nil
	}
	e, err := newEngine(cfg.StateFile, nativeHost{}, cfg.ProxyURL)
	if err != nil {
		return err
	}
	activeEngine = e
	if cfg.Enabled == nil || *cfg.Enabled {
		e.start(time.Duration(cfg.IntervalSeconds) * time.Second)
	}
	return nil
}

func handleManagement(raw []byte) (any, error) {
	var req struct {
		Method string
		Query  url.Values
		Body   []byte
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	pluginMu.Lock()
	e := activeEngine
	pluginMu.Unlock()
	if e == nil {
		return jsonResponse(503, map[string]string{"error": "plugin is unavailable"}), nil
	}
	name := req.Query.Get("name")
	if req.Method == http.MethodPut {
		var body struct {
			Name string `json:"name"`
			Rule Rule   `json:"rule"`
		}
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return jsonResponse(400, map[string]string{"error": "invalid JSON"}), nil
		}
		name = body.Name
		if err := e.set(context.Background(), name, body.Rule); err != nil {
			return jsonResponse(400, map[string]string{"error": err.Error()}), nil
		}
	}
	if name == "" {
		return jsonResponse(400, map[string]string{"error": "name is required"}), nil
	}
	rule, observation := e.get(name)
	return jsonResponse(200, map[string]any{"rule": rule, "observation": observation}), nil
}

func jsonResponse(status int, value any) any {
	body, _ := json.Marshal(value)
	return struct {
		StatusCode int
		Headers    http.Header
		Body       []byte
	}{status, http.Header{"Content-Type": {"application/json; charset=utf-8"}}, body}
}

type nativeHost struct{}

func (nativeHost) List() ([]AuthFile, error) {
	raw, err := callHost("host.auth.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Files []AuthFile `json:"files"`
	}
	err = json.Unmarshal(raw, &result)
	return result.Files, err
}

func (nativeHost) Read(index string) (map[string]json.RawMessage, error) {
	raw, err := callHost("host.auth.get", map[string]string{"auth_index": index})
	if err != nil {
		return nil, err
	}
	var result struct {
		JSON map[string]json.RawMessage `json:"json"`
	}
	err = json.Unmarshal(raw, &result)
	if err == nil && result.JSON == nil {
		err = fmt.Errorf("empty credential JSON")
	}
	return result.JSON, err
}

func (nativeHost) Save(name string, credential map[string]json.RawMessage) error {
	_, err := callHost("host.auth.save", map[string]any{"name": name, "json": credential})
	return err
}

func callHost(method string, value any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	cPayload := C.CBytes(payload)
	defer C.free(cPayload)
	var response C.cliproxy_buffer
	code := C.call_host(cMethod, (*C.uint8_t)(cPayload), C.size_t(len(payload)), &response)
	if response.ptr == nil {
		return nil, fmt.Errorf("host callback %s returned no response", method)
	}
	defer C.free_host(response.ptr, response.len)
	var env envelope
	if err := json.Unmarshal(C.GoBytes(response.ptr, C.int(response.len)), &env); err != nil {
		return nil, err
	}
	if !env.OK || code != 0 {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	return env.Result, nil
}
