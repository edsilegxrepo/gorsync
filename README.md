# rsync Operational & Product Guide (`README.md`)

This document provides product documentation, security assessment metrics, CLI argument references, usage examples, deployment configurations, and operational workflows for `rsync`.

> [!NOTE]
> Detailed technical specifications are maintained in [ARCHITECTURE.md](ARCHITECTURE.md), and test architecture/coverage reports are maintained in [TESTING.md](TESTING.md). Those documents remain authoritative.

---

## 1. Application Overview and Objectives

`gorsync` is a pure Go implementation of the `rsync` file synchronization protocol suite, offering:
- **`gorsync` CLI**: A fast, cross-platform client for local and network file synchronization.
- **`gorsyncd` Daemon**: A secure rsync daemon capable of running as a standalone service, systemd socket-activated service, or embedded container daemon.
- **`rsyncclient` & `WritableFS` Libraries**: Go packages allowing developers to embed rsync synchronization directly into Go applications or connect to custom storage engines (e.g. S3, database, or in-memory targets).

### Core Objectives
1. **Memory Safety & Portability**: Eliminate C-based buffer overflow vulnerabilities by providing a 100% Go implementation that runs seamlessly across Windows and Linux.
2. **Hermetic Security**: Enforce OS-level directory sandboxing (`os.Root` on Windows/unprivileged Linux, `pivot_root` on root Linux) so clients cannot break out of exposed modules.
3. **Zero CGO Dependencies**: Build single, static binaries with zero external C dynamic library dependencies.

---

## 2. Security Assessment

### Security Feature Matrix

| Security Domain | Implementation & Controls | Authoritative Spec Link |
| :--- | :--- | :--- |
| **Encryption in Transit** | Supported over SSH transport pipes (`rsync -e ssh`) or IPSec/TLS tunnels. Plaintext TCP connections default to port 873. | [ARCHITECTURE.md#1-architecture-and-design-choices](ARCHITECTURE.md#1-architecture-and-design-choices) |
| **Secret Management** | Challenge-Response MD4 authentication. Passwords reside in `rsyncd.secrets` (`0600`), `--password-file`, or `RSYNC_PASSWORD`. Plaintext passwords are **never sent across the wire**. | [ARCHITECTURE.md#5-security-architecture](ARCHITECTURE.md#5-security-architecture) |
| **Authentication Config** | Module-level `auth_users` challenge-response. Failed authentication attempts terminate the session immediately. | [ARCHITECTURE.md#5-security-architecture](ARCHITECTURE.md#5-security-architecture) |
| **Access Control (RBAC)** | Module-level `read only = true/false` rules, Landlock ACL restrictions on Linux, and user access validation. | [ARCHITECTURE.md#5-security-architecture](ARCHITECTURE.md#5-security-architecture) |
| **Unprivileged Context** | Runs safely as an unprivileged user process. Uses Go 1.24+ `os.OpenRoot` handles to trap path traversal (`../`) and out-of-bounds symlinks. | [ARCHITECTURE.md#5-security-architecture](ARCHITECTURE.md#5-security-architecture) |
| **Dependency Audit** | 100% Go code with zero CGO dependencies. Dependencies restricted to audited standard packages and minimal subpackages (`x/sys`, `x/crypto/md4`, `BurntSushi/toml`). | [ARCHITECTURE.md#4-dependencies](ARCHITECTURE.md#4-dependencies) |

---

## 3. Code Quality Assessment & Best Practices

- **Test Coverage**: Exceeds the workspace target with **>84.0% total engine statement coverage** (100% on `version`, 88.9% on `rsyncchecksum`, 86.8% on `rsyncwire`, 86.4% on `rsyncclient`, 84.6% on `receiver`, 83.8% on `rsyncd`, 83.6% on `maincmd`).
- **Surgical Mutation Safety**: All code modifications are strictly scoped to avoid line deletion or regression bugs.
- **Cross-Platform Parity**: Tested via automated end-to-end interop suites across 4 dataflow topologies (`Win Client -> Win Server`, `Linux Client -> Linux Server`, `Win Client -> Linux Server`, `Linux Client -> Win Server`).

> [!TIP]
> For complete package coverage tables and test execution steps, see [TESTING.md#5-code-coverage-report](TESTING.md#5-code-coverage-report).

---

## 4. Command Line Arguments Reference

### `gorsync` CLI Options

| Flag / Option | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `-a`, `--archive` | `bool` | `false` | Enables archive mode (equivalent to `-r -l -p -t -g -o -D`). |
| `-v`, `--verbose` | `bool` | `false` | Enables verbose transfer logging. |
| `-r`, `--recursive` | `bool` | `false` | Recurses into subdirectories during transfer. |
| `-l`, `--links` | `bool` | `false` | Preserves symbolic links as symlinks on destination. |
| `-p`, `--perms` | `bool` | `false` | Preserves destination file mode permissions (`0755`/`0644`). |
| `-t`, `--times` | `bool` | `false` | Preserves file modification timestamps (`mtime`). |
| `-g`, `--group` | `bool` | `false` | Preserves group ownership (requires privileged execution). |
| `-o`, `--owner` | `bool` | `false` | Preserves user ownership (requires privileged execution). |
| `-H`, `--hard-links` | `bool` | `false` | Preserves hard links between target files. |
| `-n`, `--dry-run` | `bool` | `false` | Performs a simulation run without writing to target disk. |
| `-P`, `--progress` | `bool` | `false` | Displays transfer progress and stats. |
| `-W`, `--whole-file` | `bool` | `false` | Bypasses rolling checksum delta calculation for fast local transfers. |
| `-c`, `--checksum` | `bool` | `false` | Forces content checksum validation instead of size+mtime comparison. |
| `-u`, `--update` | `bool` | `false` | Skips files that are newer on the destination. |
| `--size-only` | `bool` | `false` | Skips transfers whenever destination file sizes match. |
| `--delete` | `bool` | `false` | Deletes extraneous files from target directory. |
| `--exclude` | `string` | `""` | Excludes files matching wildcard pattern (e.g. `--exclude=*.bak`). |
| `--parallel` | `bool` | `false` | Enables multi-threaded parallel file transfer pipeline. |
| `--workers`, `--threads` | `int` | `2*NumCPU` | Number of worker goroutines for parallel hash computation and file IO. |
| `--protocol` | `int` | `27` | Forces protocol version override (supported: 27, 30, 31, 32). |
| `--tls` | `bool` | `false` | Enables TLS transport encryption (`rsyncts://`). |
| `--tls-ca` | `string` | `""` | Path to TLS Client CA certificate file for mTLS validation. |
| `--tls-cert` | `string` | `""` | Path to TLS client/server certificate file. |
| `--tls-key` | `string` | `""` | Path to TLS client/server private key file. |
| `--tls-insecure` | `bool` | `false` | Disables TLS certificate verification (for testing only). |
| `--bwlimit` | `int` | `0` | Rate limits bandwidth transfer in KiB/s. |
| `--timeout` | `int` | `0` | Sets socket I/O deadline in seconds to prevent stalls. |
| `--temp-dir`, `-T` | `string` | `""` | Stages incoming files inside a temporary directory before atomic rename. |
| `--password-file` | `string` | `""` | Reads daemon password from specified file path. |
| `--daemon` | `bool` | `false` | Runs `gorsync` as a background server daemon. |
| `--config` | `string` | `""` | Path to TOML configuration file when running in `--daemon` mode. |

---

## 5. Usage & Deployment Examples

### Example 1: Synchronizing Local Directories

```bash
gorsync -avP /var/log/app/ /backup/app-logs/
```

#### Output Sample
```text
2026/08/03 14:00:00 processing src=/var/log/app/
2026/08/03 14:00:00 receiving to dest=/backup/app-logs/
app.log
         1,048,576 100%   25.42MB/s    0:00:00 (xfr#1, to-chk=2/3)
access.log
            524,288 100%   18.12MB/s    0:00:00 (xfr#2, to-chk=1/3)
error.log
             12,400 100%   12.10MB/s    0:00:00 (xfr#3, to-chk=0/3)
sent 1,585,420 bytes  received 340 bytes  3,171,520.00 bytes/sec
total size is 1,585,264  speedup is 1.00
```

---

### Example 2: Synchronizing Over SSH

```bash
gorsync -av -e ssh /local/docs/ user@remote.server.com:/remote/docs/
```

---

### Example 3: Running `gorsyncd` Daemon

Create configuration file `/etc/gorsyncd.toml`:

```toml
[[listener]]
gorsyncd = "127.0.0.1:873"

[[module]]
name = "public"
path = "/srv/rsync/public"
writable = false

[[module]]
name = "backups"
path = "/srv/rsync/backups"
writable = true
auth_users = ["alice"]
secrets_file = "/etc/rsyncd.secrets"
```

Start daemon:

```bash
gorsyncd --daemon --config=/etc/gorsyncd.toml
```

#### Startup Output Sample
```text
2026/08/03 14:05:00 config file /etc/gorsyncd.toml loaded
2026/08/03 14:05:00 gorsyncd server daemon, pid 14820
2026/08/03 14:05:00 environment: unprivileged
2026/08/03 14:05:00 2 rsync modules configured in total
2026/08/03 14:05:00 rsync module "public" with path /srv/rsync/public configured
2026/08/03 14:05:00 rsync module "backups" with path /srv/rsync/backups configured
2026/08/03 14:05:00 rsync daemon listening on rsync://127.0.0.1:873
```

---

### Example 4: Authenticated Daemon Transfer

Create client password file:
```bash
echo "secretpassword123" > /tmp/pass.txt
chmod 0600 /tmp/pass.txt
```

Execute authenticated transfer:
```bash
gorsync -av --password-file=/tmp/pass.txt rsync://alice@127.0.0.1:873/backups/ /tmp/restored-backups/
```

---

### Example 5: Embedded Go Library Usage (`rsyncclient`)

```go
package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/edsilegxrepo/rsync/rsyncclient"
)

func main() {
	client, err := rsyncclient.New([]string{"-a"})
	if err != nil {
		panic(err)
	}

	conn, err := net.Dial("tcp", "127.0.0.1:873")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, err := client.ListFiles(ctx, conn, "public")
	if err != nil {
		panic(err)
	}

	for _, f := range files {
		fmt.Printf("File: %s, Size: %d bytes\n", f.Name, f.Length)
	}
}
```

---

## 6. Authoritative Technical References

For deeper technical specifications, implementation details, and test coverage stats, consult the authoritative documentation files:

- 🏛️ **[ARCHITECTURE.md](ARCHITECTURE.md)**: Architectural diagrams, operational data flows, concurrency pipeline, dependency map, and security sandboxing specification.
- 🧪 **[TESTING.md](TESTING.md)**: Test suite architecture, 4-topology interop matrix, package coverage report (>83.5%), and execution guide.
