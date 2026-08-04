//go:build windows

package interop_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkWSL verifies if wsl.exe and Linux rsync binary are available.
func checkWSL(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		t.Skip("wsl.exe not found on system")
	}
	out, err := exec.Command("wsl.exe", "--cd", "/tmp", "rsync", "--version").CombinedOutput()
	if err != nil {
		t.Skipf("rsync not functional inside WSL: %v (output: %s)", err, string(out))
	}
}

func getWSLHostIP() string {
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if strings.Contains(strings.ToLower(iface.Name), "wsl") {
				if addrs, err := iface.Addrs(); err == nil {
					for _, addr := range addrs {
						if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
							return ipnet.IP.String()
						}
					}
				}
			}
		}
	}
	out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", "grep nameserver /etc/resolv.conf | head -n 1 | cut -d ' ' -f 2").Output()
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		return string(bytes.TrimSpace(out))
	}
	return "127.0.0.1"
}

func toWSLPath(winPath string) string {
	winPath = filepath.Clean(winPath)
	vol := filepath.VolumeName(winPath)
	rest := winPath[len(vol):]
	drive := strings.ToLower(strings.TrimSuffix(vol, ":"))
	rest = strings.ReplaceAll(rest, "\\", "/")
	if drive == "" {
		return rest
	}
	return fmt.Sprintf("/mnt/%s%s", drive, rest)
}

func fileHash(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(b)
	return h[:], nil
}

func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestWSLInterop_ClientAndServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping WSL interop test in short mode")
	}
	checkWSL(t)

	tmpDir := t.TempDir()

	// 1. Get Windows rsync and rsyncd binaries
	rsyncBin := getCompiledRsync(t)
	rsyncdBin := getCompiledRsyncd(t)
	_ = rsyncdBin

	// Create test dataset
	sourceDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(filepath.Join(sourceDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "file1.txt"), []byte("hello from windows 12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "file2.bak"), []byte("ignore backup file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "subdir", "file3.txt"), []byte("nested content in wsl test"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("WSL Client -> rsync Daemon (Upload & List)", func(t *testing.T) {
		port := getFreePort(t)
		destDir := filepath.Join(tmpDir, "rsyncd_dest")
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create rsyncd config file
		cfgPath := filepath.Join(tmpDir, "rsyncd.toml")
		cfgContent := fmt.Sprintf(`
[[listener]]
rsyncd = "0.0.0.0:%d"

[[module]]
name = "public"
path = "%s"
writable = true
`, port, strings.ReplaceAll(destDir, "\\", "\\\\"))

		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
			t.Fatal(err)
		}

		// Start rsyncd with --daemon --gokr.config
		daemonCmd := exec.Command(rsyncBin, "--daemon", "--gokr.config="+cfgPath)
		var daemonLog bytes.Buffer
		daemonCmd.Stdout = &daemonLog
		daemonCmd.Stderr = &daemonLog
		if err := daemonCmd.Start(); err != nil {
			t.Fatalf("rsyncd start: %v", err)
		}
		t.Cleanup(func() {
			if daemonCmd.Process != nil {
				_ = daemonCmd.Process.Kill()
			}
		})

		// Wait for daemon port
		if !waitForPortLog(t, port, &daemonLog) {
			t.Fatalf("Port %d not listening. Daemon Log:\n%s", port, daemonLog.String())
		}

		hostIP := getWSLHostIP()

		// 1. WSL Linux client lists modules (#list)
		wslListCmd := exec.Command("wsl.exe", "--cd", "/tmp", "rsync", fmt.Sprintf("rsync://%s:%d/", hostIP, port))
		listOut, err := wslListCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("wsl rsync #list failed: %v\nOutput: %s\nDaemon log: %s", err, string(listOut), daemonLog.String())
		}
		if !strings.Contains(string(listOut), "public") {
			t.Fatalf("Expected 'public' module in wsl rsync #list, got:\n%s", string(listOut))
		}

		// 2. WSL Linux client uploads source directory to gokr-rsyncd
		srcWSL := toWSLPath(sourceDir) + "/"
		wslSyncCmd := exec.Command("wsl.exe", "--cd", "/tmp", "rsync", "-avz", srcWSL, fmt.Sprintf("rsync://%s:%d/public/", hostIP, port))
		syncOut, err := wslSyncCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("wsl rsync upload failed: %v\nOutput: %s\nDaemon log: %s", err, string(syncOut), daemonLog.String())
		}

		// 3. Verify files written by gokr-rsyncd on Windows disk
		h1, _ := fileHash(filepath.Join(sourceDir, "file1.txt"))
		h2, err := fileHash(filepath.Join(destDir, "file1.txt"))
		if err != nil || !bytes.Equal(h1, h2) {
			t.Fatalf("File contents mismatch for file1.txt: err=%v", err)
		}
		h3, _ := fileHash(filepath.Join(sourceDir, "subdir", "file3.txt"))
		h4, err := fileHash(filepath.Join(destDir, "subdir", "file3.txt"))
		if err != nil || !bytes.Equal(h3, h4) {
			t.Fatalf("File contents mismatch for subdir/file3.txt: err=%v", err)
		}
	})

	t.Run("gokr-rsync Windows Client -> WSL Linux Daemon (Download & Filter)", func(t *testing.T) {
		port := getFreePort(t)
		wslSrcDir := fmt.Sprintf("/tmp/wsl_rsync_src_%d", port)
		wslConfFile := fmt.Sprintf("/tmp/wsl_rsyncd_%d.conf", port)

		// Create WSL test files and rsyncd config in WSL
		setupScript := fmt.Sprintf(`
mkdir -p %s/subdir
echo "wsl content 9999" > %s/file_wsl.txt
echo "should ignore" > %s/file_wsl.bak
echo "wsl sub file" > %s/subdir/sub.txt

cat << 'EOF' > %s
use chroot = no
read only = yes
[wslmod]
path = %s
comment = WSL Export Module
EOF
`, wslSrcDir, wslSrcDir, wslSrcDir, wslSrcDir, wslConfFile, wslSrcDir)

		cmdSetup := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", setupScript)
		if out, err := cmdSetup.CombinedOutput(); err != nil {
			t.Fatalf("WSL daemon setup failed: %v\nOutput: %s", err, string(out))
		}

		// Start long-running WSL daemon process from Go
		wslDaemon := exec.Command("wsl.exe", "--cd", "/tmp", "rsync", "--daemon", "--no-detach", fmt.Sprintf("--config=%s", wslConfFile), fmt.Sprintf("--port=%d", port), "--address=0.0.0.0")
		var wslLog bytes.Buffer
		wslDaemon.Stdout = &wslLog
		wslDaemon.Stderr = &wslLog
		if err := wslDaemon.Start(); err != nil {
			t.Fatalf("WSL daemon start: %v", err)
		}
		t.Cleanup(func() {
			if wslDaemon.Process != nil {
				_ = wslDaemon.Process.Kill()
			}
			cleanupScript := fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslConfFile)
			_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", cleanupScript).Run()
		})

		if !waitForPortLog(t, port, nil) {
			t.Fatalf("WSL Port %d was not listening after 5 seconds. WSL log:\n%s", port, wslLog.String())
		}

		// 1. Windows client lists modules
		clientListCmd := exec.Command(rsyncBin, fmt.Sprintf("rsync://127.0.0.1:%d/", port))
		cListOut, err := clientListCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rsync listing WSL daemon failed: %v\nOutput: %s", err, string(cListOut))
		}
		if !strings.Contains(string(cListOut), "wslmod") {
			t.Fatalf("Expected 'wslmod' in module listing, got:\n%s", string(cListOut))
		}

		// 2. Windows client downloads files with exclude filter and progress
		targetDir := filepath.Join(tmpDir, "gorsync_download")
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			t.Fatal(err)
		}

		clientSyncCmd := exec.Command(rsyncBin, "-av", "--progress", "--exclude=*.bak", fmt.Sprintf("rsync://127.0.0.1:%d/wslmod/", port), targetDir)
		cSyncOut, err := clientSyncCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rsync download from WSL daemon failed: %v\nOutput: %s", err, string(cSyncOut))
		}

		// 3. Verify downloaded files
		d1, err := os.ReadFile(filepath.Join(targetDir, "file_wsl.txt"))
		if err != nil || !strings.Contains(string(d1), "wsl content 9999") {
			t.Fatalf("Downloaded file_wsl.txt mismatch: %v, content=%q", err, string(d1))
		}
		if _, err := os.Stat(filepath.Join(targetDir, "file_wsl.bak")); !os.IsNotExist(err) {
			t.Fatalf("Excluded file_wsl.bak was copied, expected not exist")
		}
		d3, err := os.ReadFile(filepath.Join(targetDir, "subdir", "sub.txt"))
		if err != nil || !strings.Contains(string(d3), "wsl sub file") {
			t.Fatalf("Downloaded subdir/sub.txt mismatch: %v, content=%q", err, string(d3))
		}
	})

	t.Run("rsyncclient Go API -> WSL Linux Daemon", func(t *testing.T) {
		port := getFreePort(t)
		wslSrcDir := fmt.Sprintf("/tmp/wsl_api_src_%d", port)
		wslConfFile := fmt.Sprintf("/tmp/wsl_api_%d.conf", port)

		setupScript := fmt.Sprintf(`
mkdir -p %s
echo "api text content" > %s/apifile.txt

cat << 'EOF' > %s
use chroot = no
read only = yes
[apimod]
path = %s
EOF
`, wslSrcDir, wslSrcDir, wslConfFile, wslSrcDir)

		if out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", setupScript).CombinedOutput(); err != nil {
			t.Fatalf("WSL API setup failed: %v\nOutput: %s", err, string(out))
		}

		wslDaemon := exec.Command("wsl.exe", "--cd", "/tmp", "rsync", "--daemon", "--no-detach", fmt.Sprintf("--config=%s", wslConfFile), fmt.Sprintf("--port=%d", port), "--address=127.0.0.1")
		var wslLog bytes.Buffer
		wslDaemon.Stdout = &wslLog
		wslDaemon.Stderr = &wslLog
		if err := wslDaemon.Start(); err != nil {
			t.Fatalf("WSL daemon start: %v", err)
		}
		t.Cleanup(func() {
			if wslDaemon.Process != nil {
				_ = wslDaemon.Process.Kill()
			}
			cleanupScript := fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslConfFile)
			_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", cleanupScript).Run()
		})

		out, err := exec.Command(rsyncBin, "-r", "--list-only", fmt.Sprintf("rsync://127.0.0.1:%d/apimod/", port)).CombinedOutput()
		if err != nil {
			t.Fatalf("rsync --list-only against WSL daemon failed: %v\nOutput: %s", err, string(out))
		}
		if !strings.Contains(string(out), "apifile.txt") {
			t.Fatalf("Expected apifile.txt in list output, got:\n%s", string(out))
		}
	})
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find go.mod in parent directories")
		}
		dir = parent
	}
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	if !waitForPortLog(t, port, nil) {
		t.Fatalf("Port %d was not listening after 5 seconds", port)
	}
}

func waitForPortLog(t *testing.T, port int, buf *bytes.Buffer) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
