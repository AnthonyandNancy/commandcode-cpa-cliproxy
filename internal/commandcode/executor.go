package commandcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// zeroOutputError is the retryable failure reported when a turn produced no
// visible content. It maps onto 429 so CPA cools the credential and tries the
// next one (the requirement's "zero output -> retryable" rule).
type zeroOutputError struct{ message string }

func (e *zeroOutputError) Error() string   { return e.message }
func (e *zeroOutputError) StatusCode() int { return http.StatusTooManyRequests }

// downstreamError is a classified upstream failure travelling to the ABI layer.
// It keeps the request/credential scope decided by ClassifyUpstream.
type downstreamError struct {
	status  int
	code    string
	message string
}

func (e *downstreamError) Error() string   { return e.message }
func (e *downstreamError) StatusCode() int { return e.status }

// executorStreamRequest wraps executor.execute_stream: the host adds the stream
// id used to push chunks back asynchronously.
type executorStreamRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// sessionIDFromRequest reads the session identity CPA routed on. The canonical
// id is authoritative; when the host did not supply one the plugin mirrors the
// host's own header order instead of inventing an identity.
func sessionIDFromRequest(req *pluginapi.ExecutorRequest) string {
	if req.Metadata != nil {
		if value, ok := req.Metadata["canonical_session_id"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	for _, name := range []string{"X-Session-ID", "Session-Id", "Session_id", "X-Client-Request-Id"} {
		if req.Headers == nil {
			break
		}
		if value := strings.TrimSpace(req.Headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// prepareExecutor resolves the credential, device profile and envelope shared by
// both executor paths.
func prepareExecutor(req *pluginapi.ExecutorRequest, cfg RuntimeConfig) (Account, DeviceProfile, []byte, error) {
	account, handled, err := ParseAccount(req.StorageJSON)
	if err != nil {
		return Account{}, DeviceProfile{}, nil, fmt.Errorf("credential is unusable: %w", err)
	}
	if !handled {
		return Account{}, DeviceProfile{}, nil, fmt.Errorf("credential does not belong to the %s provider", ProviderID)
	}

	profile := DeriveDevice(account.APIKey, account.DeviceSalt(cfg), account.ProjectDirValue(cfg), cfg.MasqueradePlatform, cfg.MasqueradeArch)
	payload := req.Payload
	if len(payload) == 0 {
		payload = req.OriginalRequest
	}
	envelope, errBuild := BuildEnvelope(payload, sessionIDFromRequest(req), profile, cfg)
	if errBuild != nil {
		return Account{}, DeviceProfile{}, nil, errBuild
	}
	if errSize := checkEnvelopeSize(envelope, cfg.MaxBodyMB); errSize != nil {
		return Account{}, DeviceProfile{}, nil, errSize
	}
	return account, profile, envelope, nil
}

// checkEnvelopeSize enforces the configured upstream request-body ceiling before
// any pre-request or generation call. It is request-scoped: another credential
// cannot make the same body smaller.
func checkEnvelopeSize(envelope []byte, maxMB int) error {
	if maxMB <= 0 {
		return nil
	}
	limit := int64(maxMB) * 1024 * 1024
	if int64(len(envelope)) <= limit {
		return nil
	}
	return &downstreamError{
		status:  http.StatusBadRequest,
		message: fmt.Sprintf("request body is %d bytes, limit is %d bytes", len(envelope), limit),
	}
}

// initialize runs the fingerprint/lifecycle pre-requests. Failures are logged
// and ignored: a credential that cannot complete them must still be able to
// serve (the next request retries).
func initialize(ctx context.Context, account Account, profile DeviceProfile, cfg RuntimeConfig) {
	if err := EnsureInitialized(ctx, account, profile, cfg); err != nil {
		Logf("warn", "fingerprint/lifecycle pre-request failed", map[string]any{
			"account": MaskKey(account.APIKey),
			"reason":  err.Error(),
		})
	}
}

// openUpstream performs the generation call and converts a failed reply into a
// classified error. A nil response with a nil error never happens.
func openUpstream(ctx context.Context, account Account, profile DeviceProfile, sessionID string, envelope []byte, cfg RuntimeConfig) (*http.Response, *UpstreamError, error) {
	resp, errDo := DoGenerate(ctx, account, profile, sessionID, envelope, cfg)
	if errDo != nil {
		return nil, nil, errDo
	}
	if resp.StatusCode >= 400 {
		classified := ClassifyUpstream(resp.StatusCode, readAndClose(resp))
		Logf("warn", "upstream rejected the request", map[string]any{
			"account":    MaskKey(account.APIKey),
			"status":     classified.Status,
			"downstream": classified.Downstream,
			"scope":      scopeName(classified.RequestScoped),
			"code":       classified.Code,
		})
		return nil, classified, nil
	}
	return resp, nil, nil
}

func scopeName(requestScoped bool) string {
	if requestScoped {
		return "request"
	}
	return "credential"
}

// handleExecute implements executor.execute (non-streaming clients).
func handleExecute(request []byte) ([]byte, error) {
	var req pluginapi.ExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := CurrentConfig()
	account, profile, envelope, errPrepare := prepareExecutor(&req, cfg)
	if errPrepare != nil {
		return nil, errPrepare
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initialize(ctx, account, profile, cfg)

	resp, upstreamErr, errCall := openUpstream(ctx, account, profile, sessionIDFromRequest(&req), envelope, cfg)
	if errCall != nil {
		return nil, errCall
	}
	if upstreamErr != nil {
		return nil, toDownstreamError(upstreamErr)
	}
	defer func() { _ = resp.Body.Close() }()

	completionID := "chatcmpl-" + randomHex(6)
	payload, _, errFold := foldStream(resp.Body, req.Model, completionID, time.Now().Unix(), cfg.NonStreamIdleTimeout)
	if errFold != nil {
		if errors.Is(errFold, errZeroOutput) {
			Logf("warn", "upstream produced no output", map[string]any{"account": MaskKey(account.APIKey), "model": req.Model})
			return nil, &zeroOutputError{message: "upstream produced no output; reduce context or retry"}
		}
		if upstreamFailure, ok := errFold.(*UpstreamError); ok {
			return nil, toDownstreamError(upstreamFailure)
		}
		return nil, errFold
	}

	return okEnvelope(pluginapi.ExecutorResponse{Payload: payload, Headers: http.Header{}}), nil
}

// handleExecuteStream implements executor.execute_stream. The upstream handshake
// stays synchronous on purpose: a request-scoped rejection has to travel through
// the RPC error channel, because an emitted error frame carries no status and
// CPA would resend the same body to every remaining credential.
func handleExecuteStream(request []byte) ([]byte, error) {
	var req executorStreamRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := CurrentConfig()
	account, profile, envelope, errPrepare := prepareExecutor(&req.ExecutorRequest, cfg)
	if errPrepare != nil {
		return nil, errPrepare
	}

	ctx, cancel := context.WithCancel(context.Background())
	initialize(ctx, account, profile, cfg)

	sessionID := sessionIDFromRequest(&req.ExecutorRequest)
	resp, upstreamErr, errCall := openUpstream(ctx, account, profile, sessionID, envelope, cfg)
	if errCall != nil {
		cancel()
		return nil, errCall
	}
	if upstreamErr != nil {
		cancel()
		return nil, toDownstreamError(upstreamErr)
	}

	completionID := "chatcmpl-" + randomHex(6)
	created := time.Now().Unix()

	// Without an async stream id the chunks are collected and returned inline.
	if strings.TrimSpace(req.StreamID) == "" {
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		translator := newStreamTranslator(completionID, req.Model, created)
		chunks := []pluginapi.ExecutorStreamChunk{}
		errStream := translator.consumeUpstream(resp.Body, cfg.StreamIdleTimeout, func(chunk []byte) bool {
			chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: chunk})
			return true
		})
		if errStream != nil {
			return nil, classifyStreamError(errStream, translator)
		}
		if translator.upstreamErr != nil {
			return nil, toDownstreamError(translator.upstreamErr)
		}
		if !translator.sawContent && translator.usage.OutputTokens == 0 {
			return nil, &zeroOutputError{message: "upstream produced no output; reduce context or retry"}
		}
		return okEnvelope(map[string]any{"headers": http.Header{}, "chunks": chunks}), nil
	}

	translator := newStreamTranslator(completionID, req.Model, created)
	streamID := req.StreamID

	// Push chunks in the background: the first upstream byte must reach the
	// client as soon as it arrives, not after the whole answer is buffered.
	// The async pump owns the request context and the upstream body: cancelling
	// or closing them when this RPC returns would kill the live stream.
	go func() {
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		errStream := translator.consumeUpstream(resp.Body, cfg.StreamIdleTimeout, func(chunk []byte) bool {
			return EmitStream(streamID, chunk) == nil
		})
		switch {
		case errors.Is(errStream, errClientGone):
			Logf("info", "downstream client disconnected; upstream stop requested", map[string]any{"model": req.Model})
		case errStream != nil:
			Logf("warn", "upstream stream failed", map[string]any{"model": req.Model, "reason": errStream.Error()})
			EmitStreamError(streamID, classifyStreamError(errStream, translator).Error())
		default:
			if translator.upstreamErr != nil {
				EmitStreamError(streamID, toDownstreamError(translator.upstreamErr).Error())
			} else if !translator.sawContent && translator.usage.OutputTokens == 0 {
				EmitStreamError(streamID, "upstream produced no output; reduce context or retry")
			}
		}
		CloseStream(streamID)
	}()

	return okEnvelope(map[string]any{"headers": http.Header{}}), nil
}

// classifyStreamError maps a transport/idle failure onto a retryable error.
func classifyStreamError(errStream error, translator *streamTranslator) error {
	if upstreamFailure, ok := errStream.(*UpstreamError); ok {
		return toDownstreamError(upstreamFailure)
	}
	// A stalled or broken stream is retried through the credential cooldown
	// path, exactly like the reference proxy's 429 answer.
	return &zeroOutputError{message: "upstream stream failed: " + errStream.Error()}
}

func toDownstreamError(upstreamFailure *UpstreamError) error {
	return &downstreamError{
		status:  upstreamFailure.StatusCode(),
		code:    upstreamFailure.Code,
		message: upstreamFailure.Error(),
	}
}
