package commandcode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Device identity is derived deterministically from the account's API key: one
// account is always one device, across restarts and across CPA instances. The
// signal *values* are faked (the host's real machine must never reach upstream),
// but the shape and hashing follow the official CLI exactly.
const (
	// fingerprintSalt is the CLI's own root salt, used when hashing signals.
	fingerprintSalt = "command-code:device-fingerprint:v1"
	// initRefreshInterval / initJitter mirror the CLI's 8h +/- 2h re-init.
	initRefreshInterval = 8 * time.Hour
	initJitter          = 2 * time.Hour
)

type cpuEntry struct {
	Model string
	Cores int
}

var fingerprintCPUs = []cpuEntry{
	{Model: "12th Gen Intel(R) Core(TM) i7-12650H", Cores: 10},
	{Model: "12th Gen Intel(R) Core(TM) i5-12400F", Cores: 6},
	{Model: "12th Gen Intel(R) Core(TM) i9-12900K", Cores: 16},
	{Model: "13th Gen Intel(R) Core(TM) i7-13700K", Cores: 16},
	{Model: "13th Gen Intel(R) Core(TM) i5-13600K", Cores: 14},
	{Model: "13th Gen Intel(R) Core(TM) i9-13900K", Cores: 24},
	{Model: "Intel(R) Core(TM) Ultra 7 155H", Cores: 16},
	{Model: "Intel(R) Core(TM) Ultra 9 285H", Cores: 16},
	{Model: "Intel(R) Core(TM) i9-14900K", Cores: 24},
	{Model: "Intel(R) Core(TM) i7-14700K", Cores: 20},
	{Model: "AMD Ryzen 7 7800X3D", Cores: 8},
	{Model: "AMD Ryzen 9 7950X", Cores: 16},
	{Model: "AMD Ryzen 5 7600", Cores: 6},
	{Model: "AMD Ryzen 9 7900X", Cores: 12},
	{Model: "AMD Ryzen 7 5800X3D", Cores: 8},
}

var (
	fingerprintMemories  = []int{8, 16, 24, 32, 48, 64}
	fingerprintTimezones = []string{
		"America/New_York", "America/Chicago", "America/Los_Angeles", "America/Toronto",
		"Europe/London", "Europe/Berlin", "Europe/Paris", "Europe/Moscow",
		"Asia/Shanghai", "Asia/Tokyo", "Asia/Singapore", "Asia/Seoul", "Asia/Hong_Kong",
		"Australia/Sydney", "Pacific/Auckland",
	}
	fingerprintMACCounts = []int{2, 3, 4, 5}
	fingerprintOSUsers   = []string{"dev", "user", "admin", "coder", "engineer", "work"}
	fingerprintDomains   = []string{"gmail.com", "outlook.com", "qq.com", "163.com"}
)

// DeviceProfile is the faked machine one account always reports.
type DeviceProfile struct {
	MachineID   string
	MACs        []string
	HostName    string
	GitEmail    string
	OSUser      string
	CPUModel    string
	CPUCount    int
	MemoryGiB   int
	Timezone    string
	Platform    string
	Arch        string
	OSRelease   string
	IsContainer bool
	ProjectDir  string
	Thumbmark   string
	MachineHash string
	MACHashes   []string
	HostHash    string
	OSUserHash  string
	GitMailHash string
}

// fpDigest is the deterministic scoring digest: salt \0 apiKey \0 field.
func fpDigest(salt, apiKey, field string) []byte {
	sum := sha256.Sum256([]byte(salt + "\x00" + apiKey + "\x00" + field))
	return sum[:]
}

// fpPickIndex deterministically picks one candidate: the highest digest wins.
// Adding candidates later only moves the keys the new candidate beats, unlike a
// modulo over a growing pool which would rotate every account.
func fpPickIndex(apiKey, field string, count int, labelOf func(int) string) int {
	bestIdx := 0
	var bestScore []byte
	for i := 0; i < count; i++ {
		score := fpDigest("", apiKey, field+"\x00"+labelOf(i))
		if bestScore == nil || bytes.Compare(score, bestScore) > 0 {
			bestScore = score
			bestIdx = i
		}
	}
	return bestIdx
}

// signalHash mirrors the CLI's hashSignal: sha256(FP_SALT \0 lower(value)).
func signalHash(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fingerprintSalt + "\x00" + strings.ToLower(trimmed)))
	return hex.EncodeToString(sum[:])
}

// DeriveDevice builds the stable device profile for one account.
func DeriveDevice(apiKey, salt, projectDir, platform, arch string) DeviceProfile {
	key := strings.TrimSpace(apiKey)
	scored := func(field string, count int, labelOf func(int) string) int {
		return fpPickIndex(withSalt(salt, key), field, count, labelOf)
	}

	cpu := fingerprintCPUs[scored("cpu", len(fingerprintCPUs), func(i int) string {
		return fmt.Sprintf("%s|%d", fingerprintCPUs[i].Model, fingerprintCPUs[i].Cores)
	})]
	memory := fingerprintMemories[scored("mem", len(fingerprintMemories), func(i int) string {
		return fmt.Sprintf("%d", fingerprintMemories[i])
	})]
	timezone := fingerprintTimezones[scored("timezone", len(fingerprintTimezones), func(i int) string {
		return fingerprintTimezones[i]
	})]
	macCount := fingerprintMACCounts[scored("macCount", len(fingerprintMACCounts), func(i int) string {
		return fmt.Sprintf("%d", fingerprintMACCounts[i])
	})]
	osUser := fingerprintOSUsers[scored("osUser", len(fingerprintOSUsers), func(i int) string {
		return fingerprintOSUsers[i]
	})]
	mailDomain := fingerprintDomains[scored("mailDomain", len(fingerprintDomains), func(i int) string {
		return fingerprintDomains[i]
	})]

	hexOf := func(field string, n int) string {
		digest := fpDigest(salt, key, field)
		if len(digest) < n {
			n = len(digest)
		}
		return hex.EncodeToString(digest[:n])
	}

	mid := hexOf("machineId", 16)
	machineID := fmt.Sprintf("%s-%s-%s-%s-%s", mid[0:8], mid[8:12], mid[12:16], mid[16:20], mid[20:32])

	macs := make([]string, 0, macCount)
	for i := 0; i < macCount; i++ {
		raw := fpDigest(salt, key, fmt.Sprintf("mac%d", i))[:6]
		parts := make([]string, 0, len(raw))
		for _, b := range raw {
			parts = append(parts, hex.EncodeToString([]byte{b}))
		}
		macs = append(macs, strings.Join(parts, ":"))
	}
	sortStrings(macs)

	hostName := "DESKTOP-" + strings.ToUpper(hexOf("hostname", 4))
	gitEmail := fmt.Sprintf("%s.%s@%s", osUser, hexOf("gitEmail", 3), mailDomain)

	// CLI's thumbmark: FP_SALT \0 machine \0 join([machineId, macs]) — hostname and
	// cpuModel only join when machineId is empty.
	thumbSeed := []string{strings.TrimSpace(machineID), strings.Join(macs, ",")}
	thumbSeed = filterEmpty(thumbSeed)
	thumbSum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00machine\x00%s", fingerprintSalt, strings.Join(thumbSeed, "|"))))

	profile := DeviceProfile{
		MachineID:   machineID,
		MACs:        macs,
		HostName:    hostName,
		GitEmail:    gitEmail,
		OSUser:      osUser,
		CPUModel:    cpu.Model,
		CPUCount:    cpu.Cores,
		MemoryGiB:   memory,
		Timezone:    timezone,
		Platform:    firstNonEmpty(platform, DefaultPlatform),
		Arch:        firstNonEmpty(arch, DefaultArch),
		OSRelease:   "10.0.22631",
		IsContainer: false,
		ProjectDir:  firstNonEmpty(projectDir, DefaultProjectDir),
		Thumbmark:   hex.EncodeToString(thumbSum[:]),
	}
	profile.MachineHash = signalHash(profile.MachineID)
	profile.HostHash = signalHash(profile.HostName)
	profile.OSUserHash = signalHash(profile.OSUser)
	profile.GitMailHash = signalHash(profile.GitEmail)
	for _, mac := range profile.MACs {
		if hash := signalHash(mac); hash != "" {
			profile.MACHashes = append(profile.MACHashes, hash)
		}
	}
	return profile
}

// withSalt folds the (optional) rotation salt into the account key so the whole
// fleet can be moved to new devices by changing one setting.
func withSalt(salt, apiKey string) string {
	if strings.TrimSpace(salt) == "" {
		return apiKey
	}
	return strings.TrimSpace(salt) + "\x00" + apiKey
}

// ProjectSlug mirrors the CLI's slugify(workingDir) used for x-project-slug.
func (d DeviceProfile) ProjectSlug() string {
	s := strings.ToLower(strings.TrimSpace(d.ProjectDir))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "root"
	}
	return out
}

// FingerprintPayload renders the body of POST /alpha/fingerprint/record.
func (d DeviceProfile) FingerprintPayload() map[string]any {
	components := map[string]any{
		"platform":         d.Platform,
		"arch":             d.Arch,
		"osRelease":        d.OSRelease,
		"cpuModel":         d.CPUModel,
		"cpuCount":         d.CPUCount,
		"memGiB":           d.MemoryGiB,
		"isContainer":      d.IsContainer,
		"timezone":         d.Timezone,
		"runtime":          "cli",
		"collectorVersion": 1,
	}
	setIfNotEmpty(components, "machineIdHash", d.MachineHash)
	setIfNotEmpty(components, "hostnameHash", d.HostHash)
	setIfNotEmpty(components, "osUserHash", d.OSUserHash)
	setIfNotEmpty(components, "gitEmailHash", d.GitMailHash)
	if len(d.MACHashes) > 0 {
		components["macHashes"] = d.MACHashes
	}
	return map[string]any{
		"thumbmark":  d.Thumbmark,
		"components": components,
	}
}

// initThrottle tracks the next fingerprint/lifecycle refresh per account and
// collapses concurrent first requests into a single pre-request pair.
type initThrottle struct {
	mu       sync.Mutex
	next     map[string]time.Time
	inflight map[string]*initCall
}

type initCall struct {
	done chan struct{}
	err  error
}

var accountInit = &initThrottle{next: map[string]time.Time{}, inflight: map[string]*initCall{}}

// EnsureInitialized sends the fingerprint + lifecycle pre-requests when the
// account has never been initialised or the refresh window elapsed. Failures are
// reported to the caller only for logging: they must never block a generation
// request, and they do not move the refresh deadline (so the next request
// retries).
func EnsureInitialized(ctx context.Context, account Account, profile DeviceProfile, cfg RuntimeConfig) error {
	key := KeyHash(account.APIKey)
	if key == "" {
		return fmt.Errorf("account has no api key")
	}
	if !accountInit.begin(key) {
		return accountInit.wait(ctx, key)
	}

	err := sendInitRequests(ctx, account, profile, cfg)
	accountInit.finish(key, err)
	return err
}

func (t *initThrottle) begin(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if call, ok := t.inflight[key]; ok {
		// Someone else is initialising this account: wait for them.
		t.inflight[key] = call
		return false
	}
	if next, ok := t.next[key]; ok && time.Now().Before(next) {
		return false
	}
	t.inflight[key] = &initCall{done: make(chan struct{})}
	return true
}

func (t *initThrottle) wait(ctx context.Context, key string) error {
	t.mu.Lock()
	call := t.inflight[key]
	t.mu.Unlock()
	if call == nil {
		return nil
	}
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *initThrottle) finish(key string, err error) {
	t.mu.Lock()
	call := t.inflight[key]
	delete(t.inflight, key)
	if err == nil {
		t.next[key] = time.Now().Add(initRefreshInterval + jitterDuration())
	}
	t.mu.Unlock()
	if call != nil {
		call.err = err
		close(call.done)
	}
}

// resetInitState clears the throttle (tests and config changes).
func resetInitState() {
	accountInit.mu.Lock()
	accountInit.next = map[string]time.Time{}
	accountInit.inflight = map[string]*initCall{}
	accountInit.mu.Unlock()
}

// sendInitRequests performs the two pre-requests in parallel. They deliberately
// share the client (and therefore the egress path) with generation requests.
func sendInitRequests(ctx context.Context, account Account, profile DeviceProfile, cfg RuntimeConfig) error {
	client := newUpstreamClient(cfg)
	base := account.ResolvedBaseURL(cfg)
	headers := upstreamHeaders(account, profile, cfg, "", true)

	fingerprintBody, errMarshal := json.Marshal(profile.FingerprintPayload())
	if errMarshal != nil {
		return errMarshal
	}
	lifecycleBody, errMarshal := json.Marshal(map[string]any{
		"eventType": "cli_session_exists",
		"metadata": map[string]any{
			"sessionId":  "sess_" + randomHex(8),
			"cliVersion": cfg.CLIVersion,
			"mode":       account.LifecycleModeValue(cfg),
			"os":         fmt.Sprintf("%s-%s", profile.Platform, profile.Arch),
		},
	})
	if errMarshal != nil {
		return errMarshal
	}

	var (
		wg       sync.WaitGroup
		firstErr error
		mu       sync.Mutex
	)
	post := func(path string, body []byte) {
		defer wg.Done()
		req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
		if errRequest != nil {
			recordErr(&mu, &firstErr, errRequest)
			return
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, errDo := client.Do(req)
		if errDo != nil {
			recordErr(&mu, &firstErr, fmt.Errorf("%s: %w", path, errDo))
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			recordErr(&mu, &firstErr, fmt.Errorf("%s: upstream %d", path, resp.StatusCode))
		}
	}

	wg.Add(2)
	go post(EndpointFingerprint, fingerprintBody)
	go post(EndpointLifecycle, lifecycleBody)
	wg.Wait()
	return firstErr
}

func recordErr(mu *sync.Mutex, target *error, err error) {
	mu.Lock()
	defer mu.Unlock()
	if *target == nil {
		*target = err
	}
}

func jitterDuration() time.Duration {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0
	}
	var n uint64
	for _, b := range buf {
		n = n<<8 | uint64(b)
	}
	return time.Duration(n % uint64(initJitter))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(buf)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func filterEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func setIfNotEmpty(target map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		target[key] = value
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
