package commandcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// UpstreamError is a classified CommandCode failure. RequestScoped decides
// whether CPA may keep rotating credentials: a request-level fault (bad payload,
// unknown route) can never be fixed by another account, and rotating it is how
// one bad request turns into dozens of upstream calls.
type UpstreamError struct {
	// Status is the upstream status code.
	Status int
	// Downstream is the status reported to CPA (and therefore to the client).
	Downstream int
	// Type is the machine-readable error type CPA and SDKs expect.
	Type string
	// Code is the upstream's own error code when it published one.
	Code string
	// Message is a short, credential-free description.
	Message string
	// RequestScoped reports whether the failure belongs to the request.
	RequestScoped bool
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("%s: %s (upstream %d, code %s)", e.Type, e.Message, e.Status, e.Code)
	}
	return fmt.Sprintf("%s: %s (upstream %d)", e.Type, e.Message, e.Status)
}

// StatusCode reports the status the plugin RPC must carry. CPA classifies by
// status, so request-scoped faults are reported as 400 (a status CPA never
// rotates on); everything else reports the mapped downstream status.
func (e *UpstreamError) StatusCode() int {
	if e == nil {
		return 0
	}
	if e.RequestScoped {
		return http.StatusBadRequest
	}
	return e.Downstream
}

// ccStatusMap mirrors commandcode-proxy's downstream mapping.
var ccStatusMap = map[int]struct {
	Status int
	Type   string
}{
	400: {400, "invalid_request_error"},
	401: {401, "authentication_error"},
	402: {429, "rate_limit_error"}, // payment required is treated as a rate limit
	403: {401, "authentication_error"},
	404: {404, "not_found"},
	422: {400, "invalid_request_error"},
	429: {429, "rate_limit_error"},
	500: {502, "upstream_error"},
	502: {502, "upstream_error"},
	503: {503, "temporarily_unavailable"},
}

// requestScopedUpstream reports statuses no other credential can fix. The
// requirement lists 400 / 404 / 422 as request-level, and a 404 here always
// means "this route/endpoint does not exist" (a configuration problem), never a
// credential problem.
func requestScopedUpstream(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

// ClassifyUpstream converts one upstream failure into the error the executor
// reports. The upstream's own error.code is preserved for operators.
func ClassifyUpstream(status int, body []byte) *UpstreamError {
	mapped, ok := ccStatusMap[status]
	if !ok {
		mapped = struct {
			Status int
			Type   string
		}{502, "upstream_error"}
	}

	err := &UpstreamError{
		Status:        status,
		Downstream:    mapped.Status,
		Type:          mapped.Type,
		RequestScoped: requestScopedUpstream(status),
		Message:       fmt.Sprintf("CommandCode API error (%d)", status),
	}
	if len(body) > 0 {
		var parsed struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if errJSON := json.Unmarshal(body, &parsed); errJSON == nil {
			switch {
			case parsed.Error.Message != "":
				err.Message = parsed.Error.Message
			case parsed.Message != "":
				err.Message = parsed.Message
			}
			err.Code = firstNonEmpty(parsed.Error.Code, parsed.Code)
		} else {
			if text := strings.TrimSpace(string(body)); text != "" {
				err.Message = truncateRunes(text, 200)
			}
		}
	}
	return err
}

// DoGenerate posts one envelope to the CommandCode generation endpoint. The
// caller owns the response body. The request carries the credential, the
// account's device profile and — when the caller supplied one — the session
// identity CPA routed on, so upstream attributes the request to the same
// session the host already pinned.
func DoGenerate(ctx context.Context, account Account, profile DeviceProfile, sessionID string, body []byte, cfg RuntimeConfig) (*http.Response, error) {
	base := account.ResolvedBaseURL(cfg)
	if base == "" {
		return nil, fmt.Errorf("base url is empty")
	}
	reqCtx := ctx
	if cfg.RequestTimeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, cfg.RequestTimeout)
		defer cancel()
	}
	req, errRequest := http.NewRequestWithContext(reqCtx, http.MethodPost, base+EndpointGenerate, bytes.NewReader(body))
	if errRequest != nil {
		return nil, errRequest
	}
	for key, value := range upstreamHeaders(account, profile, cfg, sessionID, false) {
		req.Header.Set(key, value)
	}
	return newUpstreamClient(cfg).Do(req)
}

// readAndClose drains a bounded part of an error body and closes it, so a failed
// response never leaks the connection.
func readAndClose(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return data
}

func truncateRunes(s string, limit int) string {
	flattened := strings.Join(strings.Fields(s), " ")
	if len(flattened) <= limit {
		return flattened
	}
	return flattened[:limit] + "…"
}
