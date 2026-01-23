//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const taskName = "Paperclip"

func generateServiceConfig(config Config) {
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting executable path: %v\n", err)
		os.Exit(1)
	}

	// Build arguments string
	args := fmt.Sprintf("-port %d -poll %d", config.Port, config.PollMs)
	if config.Peers != "" {
		args += fmt.Sprintf(" -peers %s", config.Peers)
	}

	// Create XML task definition for Task Scheduler
	// This runs at user logon, restarts on failure, and runs hidden
	taskXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Paperclip P2P Clipboard Sync</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, escapeXML(execPath), escapeXML(args))

	// Write XML to temp file
	homeDir, _ := os.UserHomeDir()
	xmlPath := filepath.Join(homeDir, "paperclip-task.xml")
	if err := os.WriteFile(xmlPath, []byte(taskXML), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing task XML: %v\n", err)
		os.Exit(1)
	}

	// Delete existing task if present (ignore errors)
	exec.Command("schtasks", "/Delete", "/TN", taskName, "/F").Run()

	// Create new task using schtasks
	cmd := exec.Command("schtasks", "/Create", "/TN", taskName, "/XML", xmlPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating scheduled task: %v\n%s\n", err, output)
		os.Exit(1)
	}

	// Clean up XML file
	os.Remove(xmlPath)

	fmt.Printf("Created scheduled task: %s\n", taskName)
	fmt.Println()
	fmt.Println("The task will start automatically at next logon.")
	fmt.Println()
	fmt.Println("To start now:")
	fmt.Printf("  schtasks /Run /TN %s\n", taskName)
	fmt.Println()
	fmt.Println("To stop:")
	fmt.Printf("  schtasks /End /TN %s\n", taskName)
	fmt.Println()
	fmt.Println("To remove:")
	fmt.Printf("  schtasks /Delete /TN %s /F\n", taskName)
	fmt.Println()
	fmt.Println("To check status:")
	fmt.Printf("  schtasks /Query /TN %s\n", taskName)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
