// Package main builds the CommandCode provider as a CLIProxyAPI dynamic plugin.
//
// The binary is a C shared library (go build -buildmode=c-shared). It contains
// only the C ABI shim: every business rule lives in internal/commandcode so it
// can be tested without cgo.
//
// Build:
//
//	go build -buildmode=c-shared -o dist/commandcode.dll .
//	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o dist/commandcode.so .
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

// Wrappers so Go can drive the host function-pointer table through cgo.
static int cc_call_host(cliproxy_host_api* api, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	return api->call(api->host_ctx, method, request, request_len, response);
}
static void cc_free_host_buffer(cliproxy_host_api* api, void* ptr, size_t len) {
	api->free_buffer(ptr, len);
}

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"

	"commandcode-cliproxy-plugin/internal/commandcode"
)

var hostAPI *C.cliproxy_host_api

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	hostAPI = host
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)

	commandcode.SetHostCaller(hostCall)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorFrame("invalid_method", "method is required", 0))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}

	raw, errHandle := commandcode.HandleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorFrame("plugin_error", errHandle.Error(), statusFromError(errHandle)))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

// hostCall performs one host RPC through the function pointer table captured at
// init. It is the only bridge between the plugin and CPA.
func hostCall(method string, request []byte) ([]byte, error) {
	if hostAPI == nil || hostAPI.call == nil {
		return nil, fmt.Errorf("host API unavailable")
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var cRequest unsafe.Pointer
	var requestLen C.size_t
	if len(request) > 0 {
		cRequest = C.CBytes(request)
		defer C.free(cRequest)
		requestLen = C.size_t(len(request))
	}

	var resp C.cliproxy_buffer
	rc := C.cc_call_host(hostAPI, cMethod, (*C.uint8_t)(cRequest), requestLen, &resp)

	var out []byte
	if resp.ptr != nil && resp.len > 0 {
		out = C.GoBytes(resp.ptr, C.int(resp.len))
	}
	if resp.ptr != nil && hostAPI.free_buffer != nil {
		C.cc_free_host_buffer(hostAPI, resp.ptr, resp.len)
	}
	if rc != 0 {
		if len(out) == 0 {
			return out, fmt.Errorf("host call %s returned %d", method, int(rc))
		}
		// The failure frame is the meaningful payload: return it so the caller
		// can decode the host's error envelope.
		return out, nil
	}
	return out, nil
}

// writeResponse copies raw into the host-provided response buffer.
func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

// errorFrame renders the failure frame the host decodes. httpStatus is 0 for
// failures that must stay credential-scoped (the host then tries another
// credential) and a status code for request-scoped failures.
func errorFrame(code, message string, httpStatus int) []byte {
	if httpStatus > 0 {
		return []byte(fmt.Sprintf(`{"ok":false,"error":{"code":%q,"message":%q,"http_status":%d}}`, code, message, httpStatus))
	}
	return []byte(fmt.Sprintf(`{"ok":false,"error":{"code":%q,"message":%q}}`, code, message))
}

type statusCoder interface {
	StatusCode() int
}

// statusFromError exposes an optional HTTP status on the returned error, which
// is what stops CPA from rotating credentials for request-level faults.
func statusFromError(err error) int {
	if err == nil {
		return 0
	}
	if sc, ok := err.(statusCoder); ok && sc != nil {
		if status := sc.StatusCode(); status > 0 {
			return status
		}
	}
	return 0
}

func main() {}
