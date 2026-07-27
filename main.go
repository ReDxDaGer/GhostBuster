package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ProcessState string

const (
	StateHealthy   ProcessState = "HEALTHY"
	StateHung      ProcessState = "HUNG/ZOMBIE"
	StateClosed    ProcessState = "CLOSED"
	StateProtected ProcessState = "PROTECTED"
	StateUnknown   ProcessState = "UNKNOWN"
)

type TargetPort struct {
	Port        int
	PID         int
	State       ProcessState
	Latency     time.Duration
	ProcessName string
}

type DaemonConfig struct {
	ProtectedPorts []int
	ProbeTimeout   time.Duration
	GracePeriod    time.Duration
	DryRun         bool
	Force          bool
	Verbose        bool
}

type GhostBuster struct {
	config DaemonConfig
}

func NewGhostBuster(cfg DaemonConfig) *GhostBuster {
	return &GhostBuster{config: cfg}
}

func (gb *GhostBuster) IsProtected(port int) bool {
	for _, p := range gb.config.ProtectedPorts {
		if p == port {
			return true
		}
	}
	return false
}

func resolvePortToPID(port int) (int, string, error) {
	switch runtime.GOOS {
	case "linux":
		return resolvePortToPIDLinux(port)
	case "darwin":
		return resolvePortToPIDDarwin(port)
	default:
		return -1, "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func resolvePortToPIDLinux(port int) (int, string, error) {
	inode, err := findInodeByPort(port)
	if err != nil {
		return -1, "", err
	}
	if inode == "" {
		return -1, "", fmt.Errorf("no process found listening on port %d", port)
	}

	pid, name, err := findPIDByInode(inode)
	if err != nil {
		return -1, "", err
	}

	return pid, name, nil
}

func resolvePortToPIDDarwin(port int) (int, string, error) {
	out, err := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return -1, "", fmt.Errorf("no process found listening on port %d", port)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return -1, "", fmt.Errorf("no process found listening on port %d", port)
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return -1, "", fmt.Errorf("unexpected lsof output for port %d", port)
	}

	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		return -1, "", err
	}

	return pid, fields[0], nil
}

func findInodeByPort(port int) (string, error) {
	file, err := os.Open("/proc/net/tcp")
	if err != nil {
		return "", err
	}
	defer file.Close()

	targetPortHex := fmt.Sprintf("%04X", port)
	scanner := bufio.NewScanner(file)

	if scanner.Scan() {
	}

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}

		localAddr := fields[1]
		parts := strings.Split(localAddr, ":")
		if len(parts) != 2 {
			continue
		}

		if parts[1] == targetPortHex {
			return fields[9], nil
		}
	}

	return "", scanner.Err()
}

func findPIDByInode(inode string) (int, string, error) {
	if inode == "0" {
		return -1, "", fmt.Errorf("no inode associated")
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return -1, "", err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}

		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == "socket:["+inode+"]" {
				commBytes, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
				name := strings.TrimSpace(string(commBytes))
				return pid, name, nil
			}
		}
	}

	return -1, "", fmt.Errorf("inode %s not found in any process", inode)
}

func isCriticalProcess(name string) bool {
	critical := map[string]bool{
		"systemd":      true,
		"launchd":      true,
		"kernel_task":  true,
		"windowserver": true,
		"sshd":         true,
		"init":         true,
		"xorg":         true,
		"loginwindow":  true,
	}
	return critical[strings.ToLower(name)]
}

func (gb *GhostBuster) ProbeHealth(port int) TargetPort {
	target := TargetPort{
		Port: port,
	}

	if gb.IsProtected(port) {
		target.State = StateProtected
		return target
	}

	pid, name, err := resolvePortToPID(port)
	if err != nil {
		target.State = StateClosed
		if gb.config.Verbose {
			fmt.Printf("  [debug] Port %d: %v\n", port, err)
		}
		return target
	}

	target.PID = pid
	target.ProcessName = name

	address := fmt.Sprintf("127.0.0.1:%d", port)
	start := time.Now()

	conn, err := net.DialTimeout("tcp", address, gb.config.ProbeTimeout)
	if err != nil {
		target.State = StateHung
		return target
	}
	target.Latency = time.Since(start)
	conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), gb.config.ProbeTimeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://%s/", address), nil)
	client := http.Client{Timeout: gb.config.ProbeTimeout}

	resp, err := client.Do(req)
	if err != nil {
		target.State = StateHung
		return target
	}
	defer resp.Body.Close()

	target.State = StateHealthy
	return target
}

func (gb *GhostBuster) ReapProcess(target TargetPort) error {
	if target.PID <= 1 {
		return fmt.Errorf("refusing to kill PID %d", target.PID)
	}

	if target.State != StateHung {
		return fmt.Errorf("refusing to kill non-hung target on port %d", target.Port)
	}

	if gb.IsProtected(target.Port) {
		return fmt.Errorf("refusing to kill protected port %d", target.Port)
	}

	if isCriticalProcess(target.ProcessName) {
		return fmt.Errorf("refusing to kill critical process %s (PID %d)", target.ProcessName, target.PID)
	}

	currentPID, _, err := resolvePortToPID(target.Port)
	if err != nil || currentPID != target.PID {
		return fmt.Errorf("port %d no longer owned by PID %d, aborting kill", target.Port, target.PID)
	}

	proc, err := os.FindProcess(target.PID)
	if err != nil {
		return fmt.Errorf("failed to locate PID %d: %v", target.PID, err)
	}

	if gb.config.DryRun {
		fmt.Printf("  [dry-run] Would send SIGTERM to PID %d (%s) on port %d\n",
			target.PID, target.ProcessName, target.Port)
		return nil
	}

	fmt.Printf("  ⚠️  Sending SIGTERM to PID %d (%s) [port %d]...\n",
		target.PID, target.ProcessName, target.Port)

	err = proc.Signal(syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("SIGTERM failed: %v", err)
	}

	time.Sleep(gb.config.GracePeriod)

	if err := proc.Signal(syscall.Signal(0)); err == nil {
		fmt.Printf("  🚨 PID %d ignored SIGTERM. Escalating to SIGKILL!\n", target.PID)
		_ = proc.Signal(syscall.SIGKILL)
	}

	fmt.Printf("  ✅ Reclaimed port %d from PID %d (%s)\n",
		target.Port, target.PID, target.ProcessName)
	return nil
}

func (gb *GhostBuster) ConcurrentScan(ports []int) []TargetPort {
	results := make([]TargetPort, 0, len(ports))
	resultsChan := make(chan TargetPort, len(ports))
	portsChan := make(chan int, len(ports))

	numWorkers := 10
	if len(ports) < numWorkers {
		numWorkers = len(ports)
	}

	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range portsChan {
				resultsChan <- gb.ProbeHealth(p)
			}
		}()
	}

	for _, p := range ports {
		portsChan <- p
	}
	close(portsChan)

	wg.Wait()
	close(resultsChan)

	for res := range resultsChan {
		results = append(results, res)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Port < results[j].Port
	})

	return results
}

func parsePortList(input string) ([]int, error) {
	var ports []int
	parts := strings.Split(input, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range: %s", part)
			}
			start, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return nil, err
			}
			end, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return nil, err
			}
			if start > end {
				start, end = end, start
			}
			for i := start; i <= end; i++ {
				ports = append(ports, i)
			}
		} else {
			port, err := strconv.Atoi(part)
			if err != nil {
				return nil, err
			}
			ports = append(ports, port)
		}
	}

	return ports, nil
}

func main() {
	var (
		portList    = flag.String("ports", "8080,3000,5000,9000-9100", "Ports to scan (e.g. 8080,3000,9000-9100)")
		timeout     = flag.Duration("timeout", 800*time.Millisecond, "Probe timeout")
		grace       = flag.Duration("grace", 2*time.Second, "Grace period between SIGTERM and SIGKILL")
		dryRun      = flag.Bool("dry-run", false, "Show what would be killed without actually killing")
		force       = flag.Bool("force", false, "Skip confirmation prompt and kill immediately")
		verbose     = flag.Bool("v", false, "Verbose output")
		killZombies = flag.Bool("kill", false, "Actually kill zombie/hung processes (required for safety)")
		protected   = flag.String("protected", "22,5432,6379,80,443", "Comma-separated protected ports that will never be touched")
	)
	flag.Parse()

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		fmt.Fprintf(os.Stderr, "❌ This tool requires Linux or macOS\n")
		os.Exit(1)
	}

	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("lsof"); err != nil {
			fmt.Fprintf(os.Stderr, "❌ lsof is required on macOS but was not found in PATH\n")
			os.Exit(1)
		}
	}

	protectedPorts, err := parsePortList(*protected)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid protected ports: %v\n", err)
		os.Exit(1)
	}

	portsToScan, err := parsePortList(*portList)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid port list: %v\n", err)
		os.Exit(1)
	}

	if len(portsToScan) == 0 {
		fmt.Fprintf(os.Stderr, "No ports specified to scan\n")
		os.Exit(1)
	}

	config := DaemonConfig{
		ProtectedPorts: protectedPorts,
		ProbeTimeout:   *timeout,
		GracePeriod:    *grace,
		DryRun:         *dryRun,
		Force:          *force,
		Verbose:        *verbose,
	}

	daemon := NewGhostBuster(config)

	fmt.Println(`
    ██████╗ ██╗  ██╗ ██████╗ ███████╗████████╗██████╗ ██╗   ██╗███████╗████████╗███████╗██████╗
   ██╔════╝ ██║  ██║██╔═══██╗██╔════╝╚══██╔══╝██╔══██╗██║   ██║██╔════╝╚══██╔══╝██╔════╝██╔══██╗
   ██║  ███╗███████║██║   ██║███████╗   ██║   ██████╔╝██║   ██║███████╗   ██║   █████╗  ██████╔╝
   ██║   ██║██╔══██║██║   ██║╚════██║   ██║   ██╔══██╗██║   ██║╚════██║   ██║   ██╔══╝  ██╔══██╗
   ╚██████╔╝██║  ██║╚██████╔╝███████║   ██║   ██████╔╝╚██████╔╝███████║   ██║   ███████╗██║  ██║
    ╚═════╝ ╚═╝  ╚═╝ ╚═════╝ ╚══════╝   ╚═╝   ╚═════╝  ╚═════╝ ╚══════╝   ╚═╝   ╚══════╝╚═╝  ╚═╝
                                       👻  D A E M O N  👻
	`)

	if config.DryRun {
		fmt.Println("  🧪 DRY RUN MODE: No processes will actually be killed")
		fmt.Println()
	}

	fmt.Printf("  🔒 Protected ports: %v\n", config.ProtectedPorts)
	fmt.Printf("  🔍 Scanning %d port(s): %s\n", len(portsToScan), *portList)
	fmt.Printf("  ⏱️  Probe timeout: %v | Grace period: %v\n", config.ProbeTimeout, config.GracePeriod)
	fmt.Println()

	startTime := time.Now()
	results := daemon.ConcurrentScan(portsToScan)
	scanDuration := time.Since(startTime)

	fmt.Printf("  ⏱️  Scan completed in %v\n\n", scanDuration)

	var zombies []TargetPort
	var healthy int
	var closed int
	var protectedCount int

	for _, res := range results {
		switch res.State {
		case StateHealthy:
			fmt.Printf("  🟢 Port %-5d | HEALTHY   | %-10s | PID %-6d | %v\n",
				res.Port, res.ProcessName, res.PID, res.Latency)
			healthy++
		case StateHung:
			fmt.Printf("  🔴 Port %-5d | HUNG      | %-10s | PID %-6d | ACTION REQUIRED\n",
				res.Port, res.ProcessName, res.PID)
			zombies = append(zombies, res)
		case StateProtected:
			fmt.Printf("  🛡️  Port %-5d | PROTECTED | (whitelisted)        | SKIPPED\n", res.Port)
			protectedCount++
		case StateClosed:
			fmt.Printf("  ⚪ Port %-5d | FREE      | (no process)         | available\n", res.Port)
			closed++
		}
	}

	fmt.Println()

	fmt.Printf("  📊 Results: %d healthy, %d free, %d protected, %d zombie(s)\n",
		healthy, closed, protectedCount, len(zombies))

	if len(zombies) == 0 {
		fmt.Println("  ✨ No zombies found. Your ports are clean!")
		return
	}

	fmt.Printf("\n  ⚠️  Found %d zombie/hung process(es)\n", len(zombies))

	if !*killZombies && !config.DryRun {
		fmt.Println()
		fmt.Println("  💡 To kill these processes, re-run with the -kill flag")
		fmt.Println("     Or use -dry-run to preview what would happen")
		return
	}

	if !config.Force && !config.DryRun {
		fmt.Print("\n  ⚡ Proceed with termination? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))
		if response != "y" && response != "yes" {
			fmt.Println("  ❌ Aborted by user")
			return
		}
	}

	fmt.Println()
	fmt.Println("  🔥 Reaping zombies...")
	fmt.Println()

	var killErrors int
	for _, z := range zombies {
		if err := daemon.ReapProcess(z); err != nil {
			fmt.Printf("  ❌ Failed to reap PID %d: %v\n", z.PID, err)
			killErrors++
		}
	}

	fmt.Println()
	if config.DryRun {
		fmt.Println("  🧪 Dry run complete. No processes were harmed.")
	} else {
		fmt.Printf("  ✅ Cleanup complete. %d process(es) reaped", len(zombies)-killErrors)
		if killErrors > 0 {
			fmt.Printf(" (%d failed)", killErrors)
		}
		fmt.Println()
	}
}
