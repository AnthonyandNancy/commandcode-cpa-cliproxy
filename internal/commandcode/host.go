package commandcode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// HostCaller performs one host RPC. The C ABI shim installs the real
// implementation at plugin init; tests install a stub, which is what makes every
// host interaction assertable without a running CPA.
type HostCaller func(method string, request []byte) ([]byte, error)

var hostCallerValue HostCaller

// SetHostCaller installs the host RPC implementation. Called once from the ABI
// shim; tests may call it repeatedly.
func SetHostCaller(fn HostCaller) { hostCallerValue = fn }

// HostCall invokes a host RPC and returns its raw result.
func HostCall(method string, request []byte) ([]byte, error) {
	if hostCallerValue == nil {
		return nil, fmt.Errorf("host API unavailable")
	}
	return hostCallerValue(method, request)
}

// hostEnvelope mirrors the {ok,result,error} frame the host returns for host RPCs.
type hostEnvelope struct {
	OK     bool             `json:"ok"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *hostEnvelopeErr `json:"error,omitempty"`
}

type hostEnvelopeErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HostCallJSON calls a host RPC with a JSON request and decodes its result.
func HostCallJSON(method string, request any, out any) error {
	var body []byte
	if request != nil {
		encoded, errMarshal := json.Marshal(request)
		if errMarshal != nil {
			return errMarshal
		}
		body = encoded
	}
	raw, errCall := HostCall(method, body)
	if errCall != nil {
		return errCall
	}
	if len(raw) == 0 {
		return nil
	}
	var env hostEnvelope
	if errUnmarshal := json.Unmarshal(raw, &env); errUnmarshal != nil {
		return fmt.Errorf("host %s: bad envelope: %w", method, errUnmarshal)
	}
	if !env.OK {
		if env.Error != nil && strings.TrimSpace(env.Error.Message) != "" {
			return fmt.Errorf("host %s: %s", method, env.Error.Message)
		}
		return fmt.Errorf("host %s failed", method)
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// EmitStream pushes one chunk payload to the host stream. An error means the
// client is gone (or the stream was closed), which callers use to stop reading
// a dead upstream.
func EmitStream(streamID string, payload []byte) error {
	if strings.TrimSpace(streamID) == "" {
		return fmt.Errorf("stream id is empty")
	}
	body, _ := json.Marshal(map[string]any{"stream_id": streamID, "payload": payload})
	_, err := HostCall(pluginabi.MethodHostStreamEmit, body)
	return err
}

// EmitStreamError reports a failure on an open stream. It must use the host's
// error field: a failure forwarded as a payload is dropped by the cross-format
// translators (claude / responses), which turns it into a silent empty turn.
func EmitStreamError(streamID string, message string) {
	if strings.TrimSpace(streamID) == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"stream_id": streamID, "error": message})
	_, _ = HostCall(pluginabi.MethodHostStreamEmit, body)
}

// CloseStream closes a host stream, ignoring "already closed" failures.
func CloseStream(streamID string) {
	if strings.TrimSpace(streamID) == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"stream_id": streamID})
	_, _ = HostCall(pluginabi.MethodHostStreamClose, body)
}

// Logf writes one structured line through the host logger. Callers must never
// put credentials into fields.
func Logf(level, message string, fields map[string]any) {
	request := map[string]any{
		"level":   level,
		"message": message,
	}
	if len(fields) > 0 {
		request["fields"] = fields
	}
	_, _ = HostCall(pluginabi.MethodHostLog, mustJSON(request))
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}
