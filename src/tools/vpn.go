package tools

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/officeclaw/src/config"
)

// VPNTool allows the LLM agent to connect, disconnect, and check status
// of Windows VPN connections (e.g., Microsoft corp VPNs).
// It uses rasdial for connection management and Get-VpnConnection for status.
// When tokens are cached (silent auth), connections succeed automatically.
// When tokens expire, the tool reports the failure so the agent can notify the user.
type VPNTool struct {
	vpnNames         []string
	connectTimeout   time.Duration
	keepAliveEnabled bool
	keepAliveMinutes int
	verifyPath       string // optional UNC path to verify connectivity
}

// NewVPNTool creates a VPN management tool.
func NewVPNTool(cfg config.VPNConfig) *VPNTool {
	timeout := cfg.ConnectTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	keepAliveMinutes := cfg.KeepAliveMinutes
	if keepAliveMinutes <= 0 {
		keepAliveMinutes = 30
	}
	return &VPNTool{
		vpnNames:         cfg.VPNNames,
		connectTimeout:   time.Duration(timeout) * time.Second,
		keepAliveEnabled: cfg.KeepAliveEnabled,
		keepAliveMinutes: keepAliveMinutes,
		verifyPath:       cfg.VerifyPath,
	}
}

func (t *VPNTool) Name() string { return "vpn_control" }

func (t *VPNTool) Description() string {
	return fmt.Sprintf("Manage Windows VPN connections. The default VPN is '%s'. "+
		"Can connect, disconnect, check status, and start a keep-alive background loop. "+
		"When the user asks to 'turn on VPN' or 'connect VPN', use action 'connect' (no vpn_name needed). "+
		"Connection uses cached Entra ID tokens when available (silent auth). "+
		"If the token has expired, the connection will fail and the user must manually sign in on the VM.",
		t.vpnNames[0])
}

func (t *VPNTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"connect", "disconnect", "status", "keep_alive"},
				"description": "Action to perform: connect to VPN, disconnect, check status, or start keep-alive loop",
			},
			"vpn_name": map[string]interface{}{
				"type":        "string",
				"description": fmt.Sprintf("Name of the VPN connection. Available: %s. If omitted, defaults to the first configured VPN.", strings.Join(t.vpnNames, ", ")),
			},
		},
		"required": []string{"action"},
	}
}

type vpnArgs struct {
	Action  string `json:"action"`
	VPNName string `json:"vpn_name"`
}

func (t *VPNTool) Execute(ctx context.Context, arguments string) (string, error) {
	args, err := ParseArgs[vpnArgs](arguments)
	if err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	vpnName := args.VPNName
	if vpnName == "" {
		if len(t.vpnNames) == 0 {
			return "", fmt.Errorf("no VPN names configured")
		}
		vpnName = t.vpnNames[0]
	}

	// Validate given VPN name against allowed list
	if !t.isVPNAllowed(vpnName) {
		return "", fmt.Errorf("VPN '%s' is not in the allowed list: %s", vpnName, strings.Join(t.vpnNames, ", "))
	}

	switch args.Action {
	case "connect":
		return t.connect(ctx, vpnName)
	case "disconnect":
		return t.disconnect(ctx, vpnName)
	case "status":
		return t.status(ctx, vpnName)
	case "keep_alive":
		return t.startKeepAlive(ctx, vpnName)
	default:
		return "", fmt.Errorf("unknown action: %s", args.Action)
	}
}

func (t *VPNTool) isVPNAllowed(name string) bool {
	for _, allowed := range t.vpnNames {
		if strings.EqualFold(allowed, name) {
			return true
		}
	}
	return false
}

// connect attempts to connect to the VPN using rasdial.
// This uses cached Entra ID tokens for silent authentication.
func (t *VPNTool) connect(ctx context.Context, vpnName string) (string, error) {
	// First check if already connected
	connected, err := t.isConnected(ctx, vpnName)
	if err == nil && connected {
		result := fmt.Sprintf("VPN '%s' is already connected.", vpnName)
		if t.verifyPath != "" {
			if t.verifyAccess() {
				result += fmt.Sprintf(" Verified access to %s.", t.verifyPath)
			} else {
				result += fmt.Sprintf(" Warning: connected but cannot reach %s.", t.verifyPath)
			}
		}
		return result, nil
	}

	log.Printf("[vpn] Attempting to connect to VPN: %s", vpnName)

	// Use rasdial to connect — this leverages cached Entra ID tokens
	cmdCtx, cancel := context.WithTimeout(ctx, t.connectTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "rasdial", vpnName)
	output, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	if err != nil {
		log.Printf("[vpn] rasdial failed for %s: %v, output: %s", vpnName, err, outputStr)
		return "", fmt.Errorf("VPN connection failed (the Entra ID token may have expired — manual sign-in required on the VM). rasdial output: %s", outputStr)
	}

	log.Printf("[vpn] rasdial succeeded for %s: %s", vpnName, outputStr)

	// Wait a moment for the connection to stabilize
	time.Sleep(3 * time.Second)

	// Verify the connection is up
	connected, _ = t.isConnected(ctx, vpnName)
	result := fmt.Sprintf("VPN '%s' connected successfully. rasdial output: %s", vpnName, outputStr)

	if !connected {
		result = fmt.Sprintf("rasdial reported success but VPN '%s' status is not 'Connected'. Output: %s", vpnName, outputStr)
	}

	// Optionally verify network share access
	if t.verifyPath != "" {
		if t.verifyAccess() {
			result += fmt.Sprintf(" Verified access to %s.", t.verifyPath)
		} else {
			result += fmt.Sprintf(" Warning: VPN connected but cannot reach %s yet (may need a few more seconds).", t.verifyPath)
		}
	}

	return result, nil
}

// disconnect terminates the VPN connection.
func (t *VPNTool) disconnect(ctx context.Context, vpnName string) (string, error) {
	log.Printf("[vpn] Disconnecting VPN: %s", vpnName)

	cmd := exec.CommandContext(ctx, "rasdial", vpnName, "/disconnect")
	output, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	if err != nil {
		return "", fmt.Errorf("VPN disconnect failed: %s", outputStr)
	}

	return fmt.Sprintf("VPN '%s' disconnected. Output: %s", vpnName, outputStr), nil
}

// status checks the current state of the VPN connection.
func (t *VPNTool) status(ctx context.Context, vpnName string) (string, error) {
	connected, err := t.isConnected(ctx, vpnName)
	if err != nil {
		return "", fmt.Errorf("checking VPN status: %w", err)
	}

	var sb strings.Builder
	if connected {
		sb.WriteString(fmt.Sprintf("VPN '%s': Connected", vpnName))
	} else {
		sb.WriteString(fmt.Sprintf("VPN '%s': Disconnected", vpnName))
	}

	// Check network share access if configured
	if t.verifyPath != "" {
		if t.verifyAccess() {
			sb.WriteString(fmt.Sprintf(". Internal path %s: Accessible", t.verifyPath))
		} else {
			sb.WriteString(fmt.Sprintf(". Internal path %s: Not accessible", t.verifyPath))
		}
	}

	return sb.String(), nil
}

// startKeepAlive launches a background goroutine that periodically checks
// and reconnects the VPN if it has dropped (silent auth with cached token).
func (t *VPNTool) startKeepAlive(ctx context.Context, vpnName string) (string, error) {
	if !t.keepAliveEnabled {
		return "Keep-alive is disabled in configuration. Set vpn.keep_alive_enabled to true.", nil
	}

	interval := time.Duration(t.keepAliveMinutes) * time.Minute
	log.Printf("[vpn] Starting keep-alive for VPN '%s' every %v", vpnName, interval)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Printf("[vpn] Keep-alive stopped for VPN '%s' (context cancelled)", vpnName)
				return
			case <-ticker.C:
				connected, err := t.isConnected(context.Background(), vpnName)
				if err != nil {
					log.Printf("[vpn] Keep-alive: error checking VPN status: %v", err)
					continue
				}
				if !connected {
					log.Printf("[vpn] Keep-alive: VPN '%s' is disconnected, attempting reconnect...", vpnName)
					result, err := t.connect(context.Background(), vpnName)
					if err != nil {
						log.Printf("[vpn] Keep-alive: reconnect failed: %v", err)
					} else {
						log.Printf("[vpn] Keep-alive: reconnect result: %s", result)
					}
				} else {
					log.Printf("[vpn] Keep-alive: VPN '%s' is still connected", vpnName)
				}
			}
		}
	}()

	return fmt.Sprintf("Keep-alive started for VPN '%s'. Will check and reconnect every %d minutes.", vpnName, t.keepAliveMinutes), nil
}

// isConnected checks VPN connection status using PowerShell Get-VpnConnection.
func (t *VPNTool) isConnected(ctx context.Context, vpnName string) (bool, error) {
	psCmd := fmt.Sprintf(`(Get-VpnConnection -Name '%s').ConnectionStatus`, vpnName)
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("Get-VpnConnection failed: %w, output: %s", err, string(output))
	}

	status := strings.TrimSpace(string(output))
	return strings.EqualFold(status, "Connected"), nil
}

// verifyAccess checks whether the configured verify path is reachable.
func (t *VPNTool) verifyAccess() bool {
	psCmd := fmt.Sprintf(`Test-Path '%s'`, t.verifyPath)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "True"
}
