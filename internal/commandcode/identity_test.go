package commandcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestAccount(baseURL string) Account {
	return Account{APIKey: "user_test1234567890", BaseURL: baseURL}
}

func TestDeriveDeviceIsDeterministic(t *testing.T) {
	cfg := DefaultConfig()
	first := DeriveDevice("user_abc", "salt-a", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)
	second := DeriveDevice("user_abc", "salt-a", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)
	if !sameProfile(first, second) {
		t.Fatalf("same key + salt produced different devices:\n%+v\n%+v", first, second)
	}

	other := DeriveDevice("user_def", "salt-a", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)
	if other.MachineID == first.MachineID || other.Thumbmark == first.Thumbmark {
		t.Fatal("different keys must not share a device identity")
	}

	rotated := DeriveDevice("user_abc", "salt-b", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)
	if rotated.MachineID == first.MachineID {
		t.Fatal("changing the fingerprint salt must move the account to another device")
	}
}

func TestDeriveDeviceShape(t *testing.T) {
	cfg := DefaultConfig()
	profile := DeriveDevice("user_shape", "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)

	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(profile.MachineID) {
		t.Errorf("machineID = %q, want the Windows MachineGuid shape", profile.MachineID)
	}
	if len(profile.MACs) < 2 || len(profile.MACs) > 5 {
		t.Errorf("MAC count = %d, want 2..5", len(profile.MACs))
	}
	macPattern := regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
	for i, mac := range profile.MACs {
		if !macPattern.MatchString(mac) {
			t.Errorf("MAC[%d] = %q, want xx:xx:xx:xx:xx:xx", i, mac)
		}
		if i > 0 && profile.MACs[i-1] > mac {
			t.Errorf("MACs are not sorted: %v", profile.MACs)
		}
	}
	if !regexp.MustCompile(`^DESKTOP-[0-9A-F]{8}$`).MatchString(profile.HostName) {
		t.Errorf("hostName = %q, want DESKTOP-xxxxxxxx", profile.HostName)
	}
	if profile.Platform != DefaultPlatform || profile.Arch != DefaultArch {
		t.Errorf("platform/arch = %q/%q, want %q/%q", profile.Platform, profile.Arch, DefaultPlatform, DefaultArch)
	}
	if profile.ProjectDir != DefaultProjectDir {
		t.Errorf("projectDir = %q, want %q", profile.ProjectDir, DefaultProjectDir)
	}
	if len(profile.Thumbmark) != 64 {
		t.Errorf("thumbmark = %q, want a sha256 hex string", profile.Thumbmark)
	}
	if !strings.HasPrefix(profile.GitEmail, profile.OSUser+".") {
		t.Errorf("gitEmail = %q, want it derived from osUser %q", profile.GitEmail, profile.OSUser)
	}
}

func TestProjectSlug(t *testing.T) {
	profile := DeviceProfile{ProjectDir: `C:\Users\dev\projects\app`}
	if got, want := profile.ProjectSlug(), "c-users-dev-projects-app"; got != want {
		t.Errorf("ProjectSlug() = %q, want %q", got, want)
	}
	if got := (DeviceProfile{ProjectDir: "---"}).ProjectSlug(); got != "root" {
		t.Errorf("ProjectSlug(empty) = %q, want root", got)
	}
}

func TestFingerprintPayloadShape(t *testing.T) {
	profile := DeriveDevice("user_payload", "", DefaultProjectDir, DefaultPlatform, DefaultArch)
	payload := profile.FingerprintPayload()

	if payload["thumbmark"] != profile.Thumbmark {
		t.Errorf("thumbmark = %v, want %q", payload["thumbmark"], profile.Thumbmark)
	}
	components, ok := payload["components"].(map[string]any)
	if !ok {
		t.Fatalf("components = %T, want a map", payload["components"])
	}
	for _, key := range []string{"machineIdHash", "hostnameHash", "osUserHash", "gitEmailHash", "macHashes", "platform", "arch", "runtime", "collectorVersion"} {
		if _, exists := components[key]; !exists {
			t.Errorf("components[%q] is missing", key)
		}
	}
	if components["runtime"] != "cli" || components["collectorVersion"] != 1 {
		t.Errorf("runtime/collectorVersion = %v/%v", components["runtime"], components["collectorVersion"])
	}
	// The CLI hashes lower-cased signal values.
	if got, want := signalHash("DESKTOP-ABC"), signalHash("desktop-abc"); got != want {
		t.Errorf("signalHash is case sensitive: %q vs %q", got, want)
	}
	if got := signalHash("   "); got != "" {
		t.Errorf("signalHash(blank) = %q, want empty so the key is omitted", got)
	}
}

func TestEnsureInitializedSendsPreRequests(t *testing.T) {
	resetInitState()
	t.Cleanup(resetInitState)

	var mu sync.Mutex
	seen := map[string]map[string]any{}
	headers := map[string]string{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen[r.URL.Path] = body
		headers["authorization"] = r.Header.Get("Authorization")
		headers["cli-environment"] = r.Header.Get("x-cli-environment")
		headers["version"] = r.Header.Get("x-command-code-version")
		headers["session"] = r.Header.Get("x-session-id")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfg := DefaultConfig()
	account := newTestAccount(server.URL)
	profile := DeriveDevice(account.APIKey, "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)

	if err := EnsureInitialized(context.Background(), account, profile, cfg); err != nil {
		t.Fatalf("EnsureInitialized() error = %v", err)
	}

	fingerprint, ok := seen[EndpointFingerprint]
	if !ok {
		t.Fatalf("fingerprint pre-request missing; saw %v", keysOf(seen))
	}
	if fingerprint["thumbmark"] != profile.Thumbmark {
		t.Errorf("fingerprint thumbmark = %v", fingerprint["thumbmark"])
	}
	lifecycle, ok := seen[EndpointLifecycle]
	if !ok {
		t.Fatalf("lifecycle pre-request missing; saw %v", keysOf(seen))
	}
	if lifecycle["eventType"] != "cli_session_exists" {
		t.Errorf("eventType = %v", lifecycle["eventType"])
	}
	metadata, _ := lifecycle["metadata"].(map[string]any)
	if metadata == nil {
		t.Fatalf("lifecycle metadata = %T", lifecycle["metadata"])
	}
	sessionID, _ := metadata["sessionId"].(string)
	if !strings.HasPrefix(sessionID, "sess_") || len(sessionID) != len("sess_")+16 {
		t.Errorf("sessionId = %q, want sess_<16 hex>", sessionID)
	}
	if metadata["cliVersion"] != cfg.CLIVersion || metadata["mode"] != DefaultLifecycleMode {
		t.Errorf("lifecycle metadata = %v", metadata)
	}
	if metadata["os"] != DefaultPlatform+"-"+DefaultArch {
		t.Errorf("lifecycle os = %v", metadata["os"])
	}

	if headers["authorization"] != "Bearer "+account.APIKey {
		t.Errorf("Authorization = %q", headers["authorization"])
	}
	if headers["cli-environment"] != "production" {
		t.Errorf("x-cli-environment = %q", headers["cli-environment"])
	}
	if headers["session"] != "" {
		t.Errorf("pre-requests must not carry a session header, got %q", headers["session"])
	}
}

func TestEnsureInitializedThrottlesAndSingleFlights(t *testing.T) {
	resetInitState()
	t.Cleanup(resetInitState)

	var count int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&count, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	account := newTestAccount(server.URL)
	profile := DeriveDevice(account.APIKey, "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)

	if err := EnsureInitialized(context.Background(), account, profile, cfg); err != nil {
		t.Fatalf("first EnsureInitialized() error = %v", err)
	}
	afterFirst := atomic.LoadInt64(&count)
	if afterFirst != 2 {
		t.Fatalf("pre-request count = %d, want 2", afterFirst)
	}

	// Inside the refresh window nothing is sent again.
	if err := EnsureInitialized(context.Background(), account, profile, cfg); err != nil {
		t.Fatalf("second EnsureInitialized() error = %v", err)
	}
	if got := atomic.LoadInt64(&count); got != afterFirst {
		t.Fatalf("throttle failed: count = %d, want %d", got, afterFirst)
	}

	// Concurrent first requests collapse into one pair.
	resetInitState()
	atomic.StoreInt64(&count, 0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = EnsureInitialized(context.Background(), account, profile, cfg)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&count); got != 2 {
		t.Errorf("concurrent pre-request count = %d, want 2 (single flight)", got)
	}
}

func TestEnsureInitializedFailureRetriesNextTime(t *testing.T) {
	resetInitState()
	t.Cleanup(resetInitState)

	var count int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&count, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	account := newTestAccount(server.URL)
	profile := DeriveDevice(account.APIKey, "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)

	if err := EnsureInitialized(context.Background(), account, profile, cfg); err == nil {
		t.Fatal("EnsureInitialized() error = nil, want the upstream failure reported")
	}
	// A failure must not move the refresh deadline: the next request retries.
	if err := EnsureInitialized(context.Background(), account, profile, cfg); err != nil {
		t.Fatalf("retry after failure error = %v", err)
	}
	if got := atomic.LoadInt64(&count); got != 4 {
		t.Errorf("pre-request count = %d, want 4 (two pairs)", got)
	}
}

func TestUpstreamHeaders(t *testing.T) {
	cfg := DefaultConfig()
	account := newTestAccount("https://example.test")
	profile := DeriveDevice(account.APIKey, "", cfg.ProjectDir, cfg.MasqueradePlatform, cfg.MasqueradeArch)

	headers := upstreamHeaders(account, profile, cfg, "", false)
	if headers["User-Agent"] != "cli" {
		t.Errorf("User-Agent = %q, want cli", headers["User-Agent"])
	}
	if headers["x-project-slug"] != profile.ProjectSlug() {
		t.Errorf("x-project-slug = %q, want the slugified project dir", headers["x-project-slug"])
	}
	if _, exists := headers["x-session-id"]; exists {
		t.Error("no session identity was supplied, so no session header may be invented")
	}
	if _, exists := headers["x-cmd-zdr"]; exists {
		t.Error("x-cmd-zdr must only be sent when ZDR is enabled")
	}

	headers = upstreamHeaders(account, profile, cfg, "session-123", false)
	if headers["x-session-id"] != "session-123" {
		t.Errorf("x-session-id = %q", headers["x-session-id"])
	}

	cfg.ZDR = true
	if headers := upstreamHeaders(account, profile, cfg, "", false); headers["x-cmd-zdr"] != "1" {
		t.Error("ZDR enabled but x-cmd-zdr missing")
	}

	if !regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`).MatchString(headers["traceparent"]) {
		t.Errorf("traceparent = %q", headers["traceparent"])
	}
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

// sameProfile compares two device profiles field by field ([]string blocks ==).
func sameProfile(a, b DeviceProfile) bool {
	if a.MachineID != b.MachineID || a.Thumbmark != b.Thumbmark || a.HostName != b.HostName {
		return false
	}
	if a.GitEmail != b.GitEmail || a.OSUser != b.OSUser || a.CPUModel != b.CPUModel || a.CPUCount != b.CPUCount {
		return false
	}
	if a.MemoryGiB != b.MemoryGiB || a.Timezone != b.Timezone || a.Platform != b.Platform || a.Arch != b.Arch {
		return false
	}
	if a.ProjectDir != b.ProjectDir || len(a.MACs) != len(b.MACs) {
		return false
	}
	for i := range a.MACs {
		if a.MACs[i] != b.MACs[i] {
			return false
		}
	}
	return true
}

var _ = time.Second
