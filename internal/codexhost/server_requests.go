package codexhost

import "encoding/json"

// denialRejection is the reason attached to review denials. It is user-facing
// in Codex transcripts, so it names the caller rather than staying generic.
const denialRejection = "agentdock: automatic denial (Codex computer use is proxied without interactive review)"

// Server-request methods that Codex app-server may send to the client. A client
// that ignores one of these stalls the running turn, so every method below is
// answered with an explicit, schema-valid denial.
const (
	methodCommandExecutionApproval = "item/commandExecution/requestApproval"
	methodFileChangeApproval       = "item/fileChange/requestApproval"
	methodToolRequestUserInput     = "item/tool/requestUserInput"
	methodMCPServerElicitation     = "mcpServer/elicitation/request"
	methodPermissionsApproval      = "item/permissions/requestApproval"
	methodDynamicToolCall          = "item/tool/call"
	methodChatGPTAuthRefresh       = "account/chatgptAuthTokens/refresh"
	methodAttestationGenerate      = "attestation/generate"
	methodApplyPatchApproval       = "applyPatchApproval"
	methodExecCommandApproval      = "execCommandApproval"
)

// codeCodexCredentialRequired marks the JSON-RPC error returned when Codex asks
// AgentDock to mint ChatGPT credentials or attestation tokens. AgentDock holds
// no such secrets; the error must stay distinguishable from a protocol fault.
const codeCodexCredentialRequired = -32050

// answerServerRequest returns the response payload for one server-initiated
// request, or a JSON-RPC error when the request cannot be satisfied.
//
// Denials are deliberately shaped exactly as the generated schema requires:
// the two legacy approval methods (applyPatchApproval, execCommandApproval)
// take a ReviewDecision object, so "denied" must be
// {"denied":{"rejection":...}} rather than the bare string.
func answerServerRequest(method string, params json.RawMessage) (any, *RPCError) {
	switch method {
	case methodCommandExecutionApproval, methodFileChangeApproval:
		return map[string]any{"decision": "decline"}, nil
	case methodApplyPatchApproval, methodExecCommandApproval:
		return map[string]any{"decision": map[string]any{"denied": map[string]any{"rejection": denialRejection}}}, nil
	case methodToolRequestUserInput:
		return map[string]any{"answers": map[string]any{}}, nil
	case methodMCPServerElicitation:
		return map[string]any{"action": "decline"}, nil
	case methodPermissionsApproval:
		return map[string]any{
			"permissions": map[string]any{"fileSystem": nil, "network": nil},
			"scope":       "turn",
		}, nil
	case methodDynamicToolCall:
		// DynamicToolCallResponse requires contentItems + success; it has no
		// "content" field. AgentDock registers no dynamic tools, so an empty
		// successful-shaped failure is the correct denial.
		return map[string]any{"contentItems": []any{}, "success": false}, nil
	case methodChatGPTAuthRefresh, methodAttestationGenerate:
		return nil, &RPCError{
			Code:    codeCodexCredentialRequired,
			Message: "AgentDock does not hold ChatGPT credentials; authenticate Codex on the host",
			Data:    paramsCarrier(params),
		}
	default:
		return nil, &RPCError{Code: -32601, Message: "Method not found: " + method}
	}
}

// paramsCarrier echoes the request parameters back as JSON-RPC error data so
// Codex logs identify which credential request failed.
func paramsCarrier(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return nil
	}
	return params
}
