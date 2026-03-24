package service

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

// Install registers OfficeClaw as a Windows service with automatic recovery.
// The service runs as the current user (not LocalSystem) so it has access to
// user-installed CLIs (Claude, Copilot), PATH, and user profile.
func Install(configPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not get executable path: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("could not get absolute path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("could not connect to service manager (run as admin): %w", err)
	}
	defer m.Disconnect()

	// Check if already installed
	s, err := m.OpenService(ServiceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists", ServiceName)
	}

	// Get current user account candidates for service logon
	candidates := getUserAccountCandidates()
	if len(candidates) == 0 {
		return fmt.Errorf("could not determine user account")
	}

	// Prompt for password
	fmt.Printf("Detected user: %s\n", candidates[0])
	fmt.Print("Enter password (required for service logon): ")
	password, err := readPassword()
	if err != nil {
		return fmt.Errorf("could not read password: %w", err)
	}

	// Build service arguments
	args := []string{}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}

	// Try each account name format until one works
	var lastErr error
	for _, accountName := range candidates {
		s, err = m.CreateService(ServiceName, exePath, mgr.Config{
			DisplayName:      "OfficeClaw AI Agent",
			Description:      "WhatsApp-integrated AI agent that monitors messages and executes tasks autonomously.",
			StartType:        mgr.StartAutomatic,
			ServiceStartName: accountName,
			Password:         password,
		}, args...)
		if err == nil {
			fmt.Printf("\nService registered with account: %s\n", accountName)
			break
		}
		lastErr = err
		fmt.Printf("\nAccount format %q failed, trying next...\n", accountName)
	}
	if s == nil {
		return fmt.Errorf("could not create service with any account format: %w", lastErr)
	}
	defer s.Close()

	// Configure automatic recovery: restart after 10s, 30s, 60s
	recoveryActions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	if err := s.SetRecoveryActions(recoveryActions, 86400); err != nil {
		// Non-fatal: service is installed but recovery settings failed
		fmt.Printf("Warning: could not set recovery actions: %v\n", err)
	}

	fmt.Printf("Service %q installed successfully.\n", ServiceName)
	fmt.Println("Recovery policy: restart after 10s, 30s, 60s on failure.")
	fmt.Println("Start with: sc start OfficeClaw")
	return nil
}

// getCurrentUserAccount returns candidate account names for service registration.
// On Azure AD/Entra joined machines, the correct format can vary, so we return
// multiple candidates to try.
func getCurrentUserAccount() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}

// getUserAccountCandidates returns possible account name formats to try.
func getUserAccountCandidates() []string {
	u, err := user.Current()
	if err != nil {
		return nil
	}

	username := u.Username
	candidates := []string{username} // Original: REDMOND\sli

	// Extract just the user part
	shortName := username
	if idx := strings.LastIndex(username, `\`); idx >= 0 {
		shortName = username[idx+1:]
	}

	// Add MACHINENAME\user format
	if hostname, err := os.Hostname(); err == nil {
		machineUser := hostname + `\` + shortName
		if !strings.EqualFold(machineUser, username) {
			candidates = append(candidates, machineUser)
		}
	}

	// Add .\user format
	dotUser := `.\` + shortName
	candidates = append(candidates, dotUser)

	return candidates
}

// readPassword reads a password from the console without echoing.
func readPassword() (string, error) {
	fd := windows.Handle(os.Stdin.Fd())

	var oldMode uint32
	if err := windows.GetConsoleMode(fd, &oldMode); err != nil {
		// Fallback: read with echo (e.g., piped input)
		var pw string
		_, err := fmt.Scanln(&pw)
		return pw, err
	}

	// Disable echo
	newMode := oldMode &^ (windows.ENABLE_ECHO_INPUT)
	if err := windows.SetConsoleMode(fd, newMode); err != nil {
		return "", err
	}
	defer windows.SetConsoleMode(fd, oldMode)

	var pw string
	_, err := fmt.Scanln(&pw)
	fmt.Println() // newline after hidden input
	return pw, err
}

// Uninstall removes the OfficeClaw Windows service.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("could not connect to service manager (run as admin): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", ServiceName, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("could not delete service: %w", err)
	}

	fmt.Printf("Service %q removed.\n", ServiceName)
	return nil
}
