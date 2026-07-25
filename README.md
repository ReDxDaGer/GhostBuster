# 👻 GhostBuster Daemon

> **Autonomous Zombie Port Reclamation for Linux Systems**

GhostBuster is a production-ready CLI tool that scans ports, detects hung/zombie processes, and safely reclaims them without touching critical system services. It combines real-time port-to-PID resolution, multi-stage health probes, and a bulletproof safety whitelist.

---

## ✨ What's Unique?

| Feature | Why It Matters |
|---------|---------------|
| **🔍 Real PID Resolution** | Reads `/proc/net/tcp` + `/proc/*/fd/` to find the *exact* process owning a port — no guessing |
| **🛡️ Bulletproof Whitelist** | Protected ports (SSH, DBs, etc.) are **completely untouchable** — hardcoded safety first |
| **⚡ Two-Stage Health Probes** | TCP dial → HTTP GET. Catches processes that accept connections but never respond |
| **🧠 Graceful → Forceful Kill** | SIGTERM first, waits, then SIGKILL only if necessary — respects your processes |
| **🧪 Dry-Run Mode** | Preview exactly what would be killed **before** anything happens |
| **🔒 Kill-Gate (`-kill` flag)** | Will **never** kill anything unless you explicitly opt-in — safe by default |
| **📊 Beautiful CLI Output** | Clean tables, color-coded states, and scan timing |

---

## 🖥️ Platform Support

| Platform | Status | Notes |
|----------|--------|-------|
| **Linux** | ✅ Fully Supported | Requires root/sudo for `/proc` access |
| **macOS** | ❌ Not Supported | No `/proc/net/tcp` equivalent |
| **Windows** | ❌ Not Supported | No `/proc` filesystem |
| **WSL** | ✅ Supported | Runs as Linux inside WSL |

> **Why Linux only?** GhostBuster resolves ports to PIDs by reading kernel-exposed data in `/proc/net/tcp` and iterating `/proc/*/fd/` symlinks. This is a Linux-specific mechanism.

---

## 📦 Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/yourusername/ghostbuster.git
cd ghostbuster

# Build the binary
go build -o ghostbuster main.go

# (Optional) Install to /usr/local/bin
sudo mv ghostbuster /usr/local/bin/
```

### Requirements

- Go 1.20+
- Linux kernel (any modern distro)
- Root/sudo privileges (for PID resolution)

---

## 🚀 Quick Start

### 1. Dry Run (Recommended First Step)

Always preview before killing:

```bash
sudo ./ghostbuster -ports "8080,3000,5000" -dry-run
```

### 2. Scan & Inspect

See what's running without any side effects:

```bash
sudo ./ghostbuster -ports "8080,3000,5000,9000-9100" -v
```

### 3. Kill Zombies

Actually terminate hung processes:

```bash
sudo ./ghostbuster -ports "8080,3000,5000" -kill
```

### 4. Force Kill (No Prompt)

Skip the confirmation for CI/CD pipelines:

```bash
sudo ./ghostbuster -ports "8080,3000" -kill -force
```

---

## 🛠️ CLI Reference

```
Usage: ghostbuster [flags]

Flags:
  -ports string
        Ports to scan (e.g. 8080,3000,9000-9100) (default "8080,3000,5000,9000-9100")

  -timeout duration
        Probe timeout for TCP/HTTP checks (default 800ms)

  -grace duration
        Grace period between SIGTERM and SIGKILL (default 2s)

  -protected string
        Comma-separated protected ports (default "22,5432,6379,80,443")

  -kill
        Actually kill zombie/hung processes (required for safety)

  -dry-run
        Show what would be killed without actually killing

  -force
        Skip confirmation prompt and kill immediately

  -v
        Verbose output with debug information
```

---

## 📋 Example Output

```
  ╔════════════════════════════════════════════════════════════╗
  ║           👻 GHOSTBUSTER: Port Zombie Reaper               ║
  ║     Kills hung processes without harming your system       ║
  ╚════════════════════════════════════════════════════════════╝

  🔒 Protected ports: [22 5432 6379 80 443]
  🔍 Scanning 6 port(s): 8080,3000,5000,5432,22,9090
  ⏱️  Probe timeout: 800ms | Grace period: 2s

  ⏱️  Scan completed in 1.245s

  🟢 Port 3000  | HEALTHY   | node       | PID 18432  | 2.1ms
  🟢 Port 8080  | HEALTHY   | python3    | PID 21501  | 5.4ms
  🔴 Port 5000  | HUNG      | java       | PID 9921   | ACTION REQUIRED
  🛡️  Port 5432  | PROTECTED | (whitelisted)        | SKIPPED
  🛡️  Port 22   | PROTECTED | (whitelisted)        | SKIPPED
  ⚪ Port 9090  | FREE      | (no process)         | available

  📊 Results: 2 healthy, 1 free, 2 protected, 1 zombie(s)

  ⚠️  Found 1 zombie/hung process(es)

  💡 To kill these processes, re-run with the -kill flag
```

---

## 🧠 How It Helps

### The Problem

- **Dev servers left running** after crashes or failed shutdowns
- **Zombie processes** that accept TCP but never respond to HTTP
- **Port conflicts** when starting new services (`Address already in use`)
- **Memory leaks** from hung applications that won't die

### The Solution

GhostBuster gives you **one command** to:

1. **Discover** which processes own which ports
2. **Diagnose** whether they're healthy, hung, or dead
3. **Reclaim** zombie ports safely without touching SSH, databases, or web servers
4. **Automate** cleanup in deployment scripts and CI pipelines

### Real-World Use Cases

| Scenario | Command |
|----------|---------|
| Clean up after a crashed dev server | `sudo ./ghostbuster -ports "3000,8080" -kill -force` |
| Nightly CI cleanup job | `sudo ./ghostbuster -ports "8000-9000" -kill -force` |
| Audit before deployment | `sudo ./ghostbuster -ports "80,443,8080" -dry-run` |
| Free up a specific stuck port | `sudo ./ghostbuster -ports "5000" -kill -force` |

---

## 🛡️ Safety Guarantees

1. **Protected ports are immutable** — even with `-force -kill`, whitelisted ports are never touched
2. **Dry-run by default** — without `-kill`, it's purely informational
3. **Kill-gate** — the `-kill` flag must be explicitly passed; no accidental terminations
4. **Graceful escalation** — SIGTERM first, SIGKILL only as a last resort
5. **System process guard** — refuses to kill PID 0 or 1 (init/kernel)

---

## 🤝 Contributing

We welcome contributions! Here's how to get started:

### 1. Fork & Clone

```bash
git clone https://github.com/yourusername/ghostbuster.git
cd ghostbuster
```

### 2. Create a Branch

```bash
git checkout -b feature/your-feature-name
```

### 3. Make Your Changes

- Follow Go conventions (`gofmt`, `golint`)
- Add tests for new functionality
- Update this README if behavior changes

### 4. Test Locally

```bash
go test ./...
go build -o ghostbuster main.go
sudo ./ghostbuster -ports "8080" -dry-run -v
```

### 5. Submit a Pull Request

- Describe what changed and why
- Reference any related issues
- Ensure CI passes

### Ideas for Contributions

- [ ] Add JSON output mode (`-json`) for scripting
- [ ] Add port auto-discovery (scan all listening ports)
- [ ] Support for UDP port checking
- [ ] Config file support (`~/.ghostbuster.yaml`)
- [ ] systemd service mode for continuous monitoring
- [ ] macOS support via `lsof` fallback
- [ ] Web dashboard for real-time monitoring

---

## 📄 License

MIT License — see [LICENSE](LICENSE) for details.

---

## 🙏 Acknowledgments

Inspired by the eternal struggle of developers everywhere fighting `Address already in use` errors at 2 AM.

> *"I ain't afraid of no zombie port."* 👻
