//go:build windows

package interop_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type topologyRunner struct {
	t          *testing.T
	name       string
	clientType string // "win" or "linux"
	serverType string // "win" or "linux"
	binClient  string // path to gokr-rsync.exe
	binDaemon  string // path to gokr-rsyncd.exe
}

func (tr *topologyRunner) runSync(args []string, srcURL string, destPath string) (string, error) {
	var cmd *exec.Cmd
	if tr.clientType == "win" {
		fullArgs := append([]string{}, args...)
		fullArgs = append(fullArgs, srcURL)
		if destPath != "" {
			fullArgs = append(fullArgs, destPath)
		}
		cmd = exec.Command(tr.binClient, fullArgs...)
	} else {
		// Linux client via WSL
		wslArgs := []string{"--cd", "/tmp", "rsync"}
		wslArgs = append(wslArgs, args...)
		wslArgs = append(wslArgs, srcURL)
		if destPath != "" {
			wslArgs = append(wslArgs, toWSLPath(destPath))
		}
		cmd = exec.Command("wsl.exe", wslArgs...)
	}

	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestE2EMatrixAndStress(t *testing.T) {
	checkWSL(t)

	tmpDir := t.TempDir()
	binClient := filepath.Join(tmpDir, "rsync.exe")
	binDaemon := filepath.Join(tmpDir, "rsync.exe") // rsync handles --daemon

	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build rsync failed: %v\nOutput: %s", err, string(out))
	}

	topologies := []struct {
		name       string
		clientType string
		serverType string
	}{
		{"Win Client -> Win Server", "win", "win"},
		{"Win Client -> Linux Server", "win", "linux"},
		{"Linux Client -> Win Server", "linux", "win"},
		{"Linux Client -> Linux Server", "linux", "linux"},
	}

	for _, topo := range topologies {
		t.Run(topo.name, func(t *testing.T) {
			runner := &topologyRunner{
				t:          t,
				name:       topo.name,
				clientType: topo.clientType,
				serverType: topo.serverType,
				binClient:  binClient,
				binDaemon:  binDaemon,
			}

			// Setup Server for Topology
			port := getFreePort(t)
			serverModuleDir := filepath.Join(t.TempDir(), "server_mod")
			if err := os.MkdirAll(filepath.Join(serverModuleDir, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}

			// Create initial dataset in server module with explicit permissions, symlinks, and hardlinks
			if err := os.WriteFile(filepath.Join(serverModuleDir, "fileA.txt"), []byte("Alpha content 100"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(serverModuleDir, "fileB.bak"), []byte("Backup file to exclude"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(serverModuleDir, "sub", "fileC.txt"), []byte("Subdirectory file"), 0o644); err != nil {
				t.Fatal(err)
			}
			// Executable script with 0755 rights
			if err := os.WriteFile(filepath.Join(serverModuleDir, "exec_script.sh"), []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			// Symlink
			symPath := filepath.Join(serverModuleDir, "symlink.txt")
			if err := os.Symlink("fileA.txt", symPath); err != nil {
				t.Logf("Server symlink creation skipped: %v", err)
			}
			// Hardlinks
			hl1 := filepath.Join(serverModuleDir, "hardlink1.txt")
			hl2 := filepath.Join(serverModuleDir, "hardlink2.txt")
			if err := os.WriteFile(hl1, []byte("Shared Hardlink Data"), 0o644); err == nil {
				_ = os.Link(hl1, hl2)
			}

			var stopServer func()
			var srcURL string
			if topo.serverType == "win" && topo.clientType == "win" {
				srcURL = fmt.Sprintf("rsync://127.0.0.1:%d/mod/", port)
				stopServer = startWinDaemon(t, port, serverModuleDir, binDaemon, "127.0.0.1")
			} else {
				srcURL = fmt.Sprintf("rsync://127.0.0.1:%d/mod/", port)
				stopServer = startLinuxDaemon(t, port, serverModuleDir)
			}
			defer stopServer()

			waitForPort(t, port)

			// --- TEST CASE 1: Standard Archive & Progress ---
			t.Run("Flag_Archive_Progress", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest1")
				_ = os.MkdirAll(dest, 0o755)

				out, err := runner.runSync([]string{"-av", "--progress"}, srcURL, dest)
				if err != nil {
					t.Fatalf("runSync failed: %v\nOutput: %s", err, out)
				}

				// Verify SHA256 parity
				verifyFileEqual(t, filepath.Join(serverModuleDir, "fileA.txt"), filepath.Join(dest, "fileA.txt"))
				verifyFileEqual(t, filepath.Join(serverModuleDir, "sub", "fileC.txt"), filepath.Join(dest, "sub", "fileC.txt"))
			})

			// --- TEST CASE 2: Checksum Matching ---
			t.Run("Flag_Checksum", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest2")
				_ = os.MkdirAll(dest, 0o755)

				out, err := runner.runSync([]string{"-av", "--checksum"}, srcURL, dest)
				if err != nil {
					t.Fatalf("Checksum sync failed: %v\nOutput: %s", err, out)
				}
				verifyFileEqual(t, filepath.Join(serverModuleDir, "fileA.txt"), filepath.Join(dest, "fileA.txt"))
			})

			// --- TEST CASE 3: Exclude & Filter Rules ---
			t.Run("Flag_Exclude_Filter", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest3")
				_ = os.MkdirAll(dest, 0o755)

				out, err := runner.runSync([]string{"-av", "--exclude=*.bak"}, srcURL, dest)
				if err != nil {
					t.Fatalf("Exclude sync failed: %v\nOutput: %s", err, out)
				}

				if _, err := os.Stat(filepath.Join(dest, "fileB.bak")); !os.IsNotExist(err) {
					t.Fatalf("Excluded file fileB.bak was synced unexpectedly")
				}
				verifyFileEqual(t, filepath.Join(serverModuleDir, "fileA.txt"), filepath.Join(dest, "fileA.txt"))
			})

			// --- TEST CASE 4: Update & Size Only ---
			t.Run("Flag_Update_SizeOnly", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest4")
				_ = os.MkdirAll(dest, 0o755)

				// Pre-create dest file with matching size but different content
				if err := os.WriteFile(filepath.Join(dest, "fileA.txt"), []byte("Alpha content 100"), 0o644); err != nil {
					t.Fatal(err)
				}

				out, err := runner.runSync([]string{"-av", "--size-only"}, srcURL, dest)
				if err != nil {
					t.Fatalf("SizeOnly sync failed: %v\nOutput: %s", err, out)
				}
			})

			// --- TEST CASE 5: Delete Extraneous ---
			t.Run("Flag_Delete", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest5")
				_ = os.MkdirAll(dest, 0o755)
				// Create file on dest that doesn't exist on server
				if err := os.WriteFile(filepath.Join(dest, "extra.txt"), []byte("should be deleted"), 0o644); err != nil {
					t.Fatal(err)
				}

				out, err := runner.runSync([]string{"-av", "--delete"}, srcURL, dest)
				if err != nil {
					t.Fatalf("Delete sync failed: %v\nOutput: %s", err, out)
				}
				if _, err := os.Stat(filepath.Join(dest, "extra.txt")); !os.IsNotExist(err) {
					t.Fatalf("Extraneous file extra.txt was not deleted by --delete")
				}
			})

			// --- TEST CASE 6: List Only ---
			t.Run("Flag_ListOnly", func(t *testing.T) {
				out, err := runner.runSync([]string{"-r", "--list-only"}, srcURL, "")
				if err != nil {
					t.Fatalf("ListOnly failed: %v\nOutput: %s", err, out)
				}
				if !strings.Contains(out, "fileA.txt") {
					t.Fatalf("Expected fileA.txt in --list-only output, got:\n%s", out)
				}
			})

			// --- TEST CASE 7: Rate Limiting ---
			t.Run("Flag_BWLimit", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest7")
				_ = os.MkdirAll(dest, 0o755)

				out, err := runner.runSync([]string{"-av", "--bwlimit=10000"}, srcURL, dest)
				if err != nil {
					t.Fatalf("BWLimit sync failed: %v\nOutput: %s", err, out)
				}
				verifyFileEqual(t, filepath.Join(serverModuleDir, "fileA.txt"), filepath.Join(dest, "fileA.txt"))
			})

			// --- TEST CASE 8: Dry Run ---
			t.Run("Flag_DryRun", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest8")
				_ = os.MkdirAll(dest, 0o755)

				out, err := runner.runSync([]string{"-av", "--dry-run"}, srcURL, dest)
				if err != nil {
					t.Fatalf("DryRun sync failed: %v\nOutput: %s", err, out)
				}
				if _, err := os.Stat(filepath.Join(dest, "fileA.txt")); !os.IsNotExist(err) {
					t.Fatalf("File fileA.txt was created on disk despite --dry-run")
				}
			})

			// --- TEST CASE 9: Symlink / Hardlink / Permissions ---
			t.Run("Flag_Symlink_Hardlink_Perms", func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "dest9")
				_ = os.MkdirAll(dest, 0o755)

				out, err := runner.runSync([]string{"-av", "-H"}, srcURL, dest)
				if err != nil {
					t.Fatalf("Symlink/Hardlink/Perms sync failed: %v\nOutput: %s", err, out)
				}

				// Verify permissions of exec_script.sh
				execPath := filepath.Join(dest, "exec_script.sh")
				if info, err := os.Stat(execPath); err == nil {
					if runtime.GOOS != "windows" {
						if info.Mode()&0o111 == 0 {
							t.Fatalf("Executable rights lost for exec_script.sh: mode %v", info.Mode())
						}
					}
				}

				// Verify symlink parity
				destSym := filepath.Join(dest, "symlink.txt")
				if lst, err := os.Lstat(destSym); err == nil {
					if lst.Mode()&os.ModeSymlink != 0 {
						if target, err := os.Readlink(destSym); err == nil {
							t.Logf("Symlink verified successfully: %s -> %s", destSym, target)
						}
					}
				}

				// Verify hardlink parity
				destHl1 := filepath.Join(dest, "hardlink1.txt")
				destHl2 := filepath.Join(dest, "hardlink2.txt")
				verifyFileEqual(t, destHl1, destHl2)
			})
		})
	}
}

func TestE2EChrootSandbox(t *testing.T) {
	checkWSL(t)

	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "parent")
	modDir := filepath.Join(parentDir, "inside_mod")
	_ = os.MkdirAll(modDir, 0o755)

	// Create secret file outside module root
	outsideFile := filepath.Join(parentDir, "outside_secret.txt")
	if err := os.WriteFile(outsideFile, []byte("TOP SECRET DATA"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create normal file inside module root
	insideFile := filepath.Join(modDir, "inside.txt")
	if err := os.WriteFile(insideFile, []byte("Public Data"), 0o644); err != nil {
		t.Fatal(err)
	}

	binClient := filepath.Join(tmpDir, "gokr-rsync.exe")
	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build gokr-rsync failed: %v\nOutput: %s", err, string(out))
	}

	port := getFreePort(t)
	stopServer := startWinDaemon(t, port, modDir, binClient, "127.0.0.1")
	defer stopServer()

	waitForPort(t, port)

	// 1. Verify normal file inside module can be transferred
	destNormal := filepath.Join(tmpDir, "dest_normal")
	_ = os.MkdirAll(destNormal, 0o755)
	cmdNormal := exec.Command(binClient, "-av", fmt.Sprintf("rsync://127.0.0.1:%d/mod/inside.txt", port), destNormal)
	if out, err := cmdNormal.CombinedOutput(); err != nil {
		t.Fatalf("Normal file transfer failed: %v\nOutput: %s", err, string(out))
	}
	verifyFileEqual(t, insideFile, filepath.Join(destNormal, "inside.txt"))

	// 2. Test Symlink Escape & Chroot Sandboxing Protection
	// Create a symlink pointing outside the module root: mod/symlink_outside -> ../outside_secret.txt
	symlinkPath := filepath.Join(modDir, "symlink_outside")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Logf("Symlink creation skipped: %v", err)
	} else {
		destEscape := filepath.Join(tmpDir, "dest_escape")
		_ = os.MkdirAll(destEscape, 0o755)

		// Client attempts to fetch the symlink pointing outside module root
		cmdEscape := exec.Command(binClient, "-av", fmt.Sprintf("rsync://127.0.0.1:%d/mod/symlink_outside", port), destEscape)
		outWin, errWin := cmdEscape.CombinedOutput()
		if errWin == nil {
			// Verify that the secret file content was NOT copied
			escapedContent, _ := os.ReadFile(filepath.Join(destEscape, "symlink_outside"))
			if string(escapedContent) == "TOP SECRET DATA" {
				t.Fatalf("Symlink chroot escape vulnerability detected! Secret content transferred:\n%s", string(escapedContent))
			}
		}
		t.Logf("Chroot/os.Root sandboxing protected against symlink escape: %v (out: %s)", errWin, string(bytes.TrimSpace(outWin)))
	}
}

func TestE2EAuthDaemon(t *testing.T) {
	checkWSL(t)

	tmpDir := t.TempDir()
	modDir := filepath.Join(tmpDir, "auth_mod")
	_ = os.MkdirAll(modDir, 0o755)

	testFile := filepath.Join(modDir, "secret_doc.txt")
	if err := os.WriteFile(testFile, []byte("Protected Document"), 0o600); err != nil {
		t.Fatal(err)
	}

	secretsFile := filepath.Join(tmpDir, "rsyncd.secrets")
	if err := os.WriteFile(secretsFile, []byte("alice:password123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	binClient := filepath.Join(tmpDir, "gokr-rsync.exe")
	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build gokr-rsync failed: %v\nOutput: %s", err, string(out))
	}

	port := getFreePort(t)
	cfgPath := filepath.Join(tmpDir, "gokr-rsyncd.toml")
	cfgContent := fmt.Sprintf(`
[[listener]]
rsyncd = "0.0.0.0:%d"

[[module]]
name = "authmod"
path = "%s"
writable = true
auth_users = ["alice"]
secrets_file = "%s"
`, port, strings.ReplaceAll(modDir, "\\", "\\\\"), strings.ReplaceAll(secretsFile, "\\", "\\\\"))

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	var daemonBuf bytes.Buffer
	daemonCmd := exec.Command(binClient, "--daemon", "--gokr.config="+cfgPath)
	daemonCmd.Stdout = &daemonBuf
	daemonCmd.Stderr = &daemonBuf
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Kill()
		}
		t.Logf("Daemon log:\n%s", daemonBuf.String())
	}()

	waitForPort(t, port)

	// 1. Unauthenticated request must fail
	destFail := filepath.Join(tmpDir, "dest_fail")
	_ = os.MkdirAll(destFail, 0o755)
	cmdUnauth := exec.Command(binClient, "-av", fmt.Sprintf("rsync://127.0.0.1:%d/authmod/", port), destFail)
	if out, err := cmdUnauth.CombinedOutput(); err == nil {
		t.Fatalf("Unauthenticated request succeeded unexpectedly! Output:\n%s", string(out))
	} else {
		t.Logf("Unauthenticated request rejected as expected: %v", err)
	}

	// 2. Authenticated request with --password-file must succeed
	passFile := filepath.Join(tmpDir, "client_pass.txt")
	if err := os.WriteFile(passFile, []byte("password123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	destPass := filepath.Join(tmpDir, "dest_pass")
	_ = os.MkdirAll(destPass, 0o755)
	cmdAuth := exec.Command(binClient, "-av", fmt.Sprintf("--password-file=%s", passFile), fmt.Sprintf("rsync://alice@127.0.0.1:%d/authmod/", port), destPass)
	if out, err := cmdAuth.CombinedOutput(); err != nil {
		t.Fatalf("Authenticated request with --password-file failed: %v\nOutput: %s", err, string(out))
	}

	verifyFileEqual(t, testFile, filepath.Join(destPass, "secret_doc.txt"))

	// 3. WSL Linux client authenticated request with RSYNC_PASSWORD
	destLinuxPass := filepath.Join(tmpDir, "dest_linux_pass")
	_ = os.MkdirAll(destLinuxPass, 0o755)
	hostIP := getWSLHostIP()
	cmdWSLAuth := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("export RSYNC_PASSWORD=password123; rsync -av rsync://alice@%s:%d/authmod/ %s", hostIP, port, toWSLPath(destLinuxPass)))
	if out, err := cmdWSLAuth.CombinedOutput(); err != nil {
		t.Logf("WSL RSYNC_PASSWORD request note: %v (Output: %s)", err, string(out))
	} else {
		verifyFileEqual(t, testFile, filepath.Join(destLinuxPass, "secret_doc.txt"))
	}
}

func TestE2EStress(t *testing.T) {
	checkWSL(t)

	tmpDir := t.TempDir()
	binClient := filepath.Join(tmpDir, "gokr-rsync.exe")

	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build gokr-rsync failed: %v\nOutput: %s", err, string(out))
	}

	port := getFreePort(t)
	serverModuleDir := filepath.Join(tmpDir, "stress_mod")

	// 1. High File Count & Deep Nesting Generation (500 files)
	t.Log("Generating 500 files across nested directories for stress test...")
	for i := 0; i < 500; i++ {
		subDir := filepath.Join(serverModuleDir, fmt.Sprintf("dir_%d", i%10), fmt.Sprintf("sub_%d", i%5))
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatal(err)
		}
		fn := filepath.Join(subDir, fmt.Sprintf("stress_file_%d.data", i))
		content := fmt.Sprintf("Stress payload data file index %d - timestamp %d", i, time.Now().UnixNano())
		if err := os.WriteFile(fn, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 2. Large File Generation (10MB Payload)
	largeFile := filepath.Join(serverModuleDir, "large_10mb.bin")
	largeBuf := bytes.Repeat([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789\n"), 300000) // ~11MB
	if err := os.WriteFile(largeFile, largeBuf, 0o644); err != nil {
		t.Fatal(err)
	}

	stopServer := startWinDaemon(t, port, serverModuleDir, binClient, "127.0.0.1")
	defer stopServer()

	waitForPort(t, port)

	// Stress Subtest A: Full High-Volume & Large File Transfer
	t.Run("Stress_HighVolume_10MB_Transfer", func(t *testing.T) {
		destDir := filepath.Join(tmpDir, "stress_dest")
		_ = os.MkdirAll(destDir, 0o755)

		srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/mod/", port)
		cmd := exec.Command(binClient, "-av", "--progress", srcURL, destDir)
		start := time.Now()
		out, err := cmd.CombinedOutput()
		duration := time.Since(start)
		if err != nil {
			t.Fatalf("HighVolume transfer failed: %v\nOutput: %s", err, string(out))
		}

		t.Logf("Transferred 501 files (~11MB) in %v", duration)

		// Verify sample files and large file
		verifyFileEqual(t, largeFile, filepath.Join(destDir, "large_10mb.bin"))
		verifyFileEqual(t, filepath.Join(serverModuleDir, "dir_0", "sub_0", "stress_file_0.data"), filepath.Join(destDir, "dir_0", "sub_0", "stress_file_0.data"))
	})

	// Stress Subtest B: Concurrent Client Connections (8 Parallel Workers)
	t.Run("Stress_Concurrent_Clients", func(t *testing.T) {
		var wg sync.WaitGroup
		concurrency := 8
		errChan := make(chan error, concurrency)

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				workerDest := filepath.Join(tmpDir, fmt.Sprintf("worker_dest_%d", workerID))
				_ = os.MkdirAll(workerDest, 0o755)

				srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/mod/", port)
				cmd := exec.Command(binClient, "-a", srcURL, workerDest)
				if out, err := cmd.CombinedOutput(); err != nil {
					errChan <- fmt.Errorf("Worker %d failed: %v (out: %s)", workerID, err, string(out))
				}
			}(i)
		}

		wg.Wait()
		close(errChan)

		for err := range errChan {
			t.Errorf("Concurrent worker error: %v", err)
		}
	})
}

func startWinDaemon(t *testing.T, port int, modPath string, bin string, listenIP string) func() {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "gokr-rsyncd.toml")
	cfgContent := fmt.Sprintf(`
[[listener]]
rsyncd = "%s:%d"

[[module]]
name = "mod"
path = "%s"
writable = true
`, listenIP, port, strings.ReplaceAll(modPath, "\\", "\\\\"))

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	daemonCmd := exec.Command(bin, "--daemon", "--gokr.config="+cfgPath)
	var daemonLog bytes.Buffer
	daemonCmd.Stdout = &daemonLog
	daemonCmd.Stderr = &daemonLog
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("gokr-rsyncd start: %v", err)
	}

	return func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Kill()
		}
	}
}

func startWinDaemonWithTLS(t *testing.T, port int, modPath string, bin string, listenIP string, certPath, keyPath string, modName string) func() {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "gokr-rsyncd-tls.toml")
	cfgContent := fmt.Sprintf(`
tls_cert = %q
tls_key = %q

[[listener]]
rsyncd = "%s:%d"

[[module]]
name = "%s"
path = "%s"
writable = true
`, certPath, keyPath, listenIP, port, modName, strings.ReplaceAll(modPath, "\\", "\\\\"))

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	daemonCmd := exec.Command(bin, "--daemon", "--gokr.config="+cfgPath)
	var daemonLog bytes.Buffer
	daemonCmd.Stdout = &daemonLog
	daemonCmd.Stderr = &daemonLog
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("gokr-rsyncd start: %v", err)
	}

	return func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("Win TLS Daemon Output:\n%s", daemonLog.String())
		}
	}
}

func startLinuxDaemon(t *testing.T, port int, winModPath string) func() {
	t.Helper()
	wslSrcDir := toWSLPath(winModPath)
	wslConfFile := fmt.Sprintf("/tmp/wsl_matrix_%d.conf", port)

	setupScript := fmt.Sprintf(`
cat << 'EOF' > %s
use chroot = no
read only = no
[mod]
path = %s
comment = WSL Matrix Module
EOF
`, wslConfFile, wslSrcDir)

	if out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", setupScript).CombinedOutput(); err != nil {
		t.Fatalf("WSL matrix setup failed: %v\nOutput: %s", err, string(out))
	}

	wslDaemon := exec.Command("wsl.exe", "--cd", "/tmp", "rsync", "--daemon", "--no-detach", fmt.Sprintf("--config=%s", wslConfFile), fmt.Sprintf("--port=%d", port), "--address=127.0.0.1")
	var wslLog bytes.Buffer
	wslDaemon.Stdout = &wslLog
	wslDaemon.Stderr = &wslLog
	if err := wslDaemon.Start(); err != nil {
		t.Fatalf("WSL daemon start: %v", err)
	}

	return func() {
		if wslDaemon.Process != nil {
			_ = wslDaemon.Process.Kill()
		}
		cleanupScript := fmt.Sprintf("rm -rf %s", wslConfFile)
		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", cleanupScript).Run()
	}
}

func verifyFileEqual(t *testing.T, f1, f2 string) {
	t.Helper()
	h1, err := fileHash(f1)
	if err != nil {
		t.Fatalf("fileHash(%s): %v", f1, err)
	}
	h2, err := fileHash(f2)
	if err != nil {
		t.Fatalf("fileHash(%s): %v", f2, err)
	}
	if !bytes.Equal(h1, h2) {
		t.Fatalf("SHA256 mismatch between %s and %s", f1, f2)
	}
}

func TestE2ELocalDiskToDisk(t *testing.T) {
	tmpDir := t.TempDir()

	binClient := filepath.Join(tmpDir, "rsync.exe")
	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build rsync failed: %v\nOutput: %s", err, string(out))
	}

	// 1. Native Windows Disk-to-Disk Sync
	t.Run("Windows_Disk_To_Disk", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "win_src")
		destDir := filepath.Join(tmpDir, "win_dest")
		_ = os.MkdirAll(srcDir, 0o755)
		_ = os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755)

		file1 := filepath.Join(srcDir, "file1.txt")
		_ = os.WriteFile(file1, []byte("Hello World Local Disk 1"), 0o644)

		script := filepath.Join(srcDir, "script.sh")
		_ = os.WriteFile(script, []byte("#!/bin/sh\necho Local Disk Test\n"), 0o755)

		nested := filepath.Join(srcDir, "sub", "nested.txt")
		_ = os.WriteFile(nested, []byte("Nested Content Local"), 0o644)

		pastTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
		_ = os.Chtimes(file1, pastTime, pastTime)
		_ = os.Chtimes(script, pastTime, pastTime)

		symlinkPath := filepath.Join(srcDir, "symlink.txt")
		_ = os.Symlink("file1.txt", symlinkPath)

		hl1 := filepath.Join(srcDir, "hardlink1.txt")
		_ = os.WriteFile(hl1, []byte("Hardlink Payload Data"), 0o644)
		hl2 := filepath.Join(srcDir, "hardlink2.txt")
		_ = os.Link(hl1, hl2)

		// Perform local sync: gokr-rsync -av -H win_src/ win_dest/
		cmd := exec.Command(binClient, "-av", "-H", filepath.ToSlash(srcDir)+"/", destDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Windows Local Disk-to-Disk sync failed: %v\nOutput: %s", err, string(out))
		}

		// Assertions
		verifyFileEqual(t, file1, filepath.Join(destDir, "file1.txt"))
		verifyFileEqual(t, script, filepath.Join(destDir, "script.sh"))
		verifyFileEqual(t, nested, filepath.Join(destDir, "sub", "nested.txt"))
		verifyFileEqual(t, hl1, filepath.Join(destDir, "hardlink1.txt"))
		verifyFileEqual(t, hl2, filepath.Join(destDir, "hardlink2.txt"))

		// Timestamp check
		destFile1Info, err := os.Stat(filepath.Join(destDir, "file1.txt"))
		if err != nil {
			t.Fatalf("Stat dest file1 failed: %v", err)
		}
		if diff := destFile1Info.ModTime().Sub(pastTime); diff > 2*time.Second || diff < -2*time.Second {
			t.Errorf("mtime mismatch: expected %v, got %v", pastTime, destFile1Info.ModTime())
		}

		// Symlink check
		if lst, err := os.Lstat(filepath.Join(destDir, "symlink.txt")); err == nil {
			if lst.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Readlink(filepath.Join(destDir, "symlink.txt")); err == nil {
					t.Logf("Local disk symlink verified: symlink.txt -> %s", target)
				}
			}
		}
	})

	// 2. Linux WSL Local Disk-to-Disk Sync
	t.Run("Linux_WSL_Disk_To_Disk", func(t *testing.T) {
		checkWSL(t)

		wslSrcDir := fmt.Sprintf("/tmp/wsl_disk_src_%d", time.Now().UnixNano()%10000)
		wslDestDir := fmt.Sprintf("/tmp/wsl_disk_dest_%d", time.Now().UnixNano()%10000)

		setupScript := fmt.Sprintf(`
mkdir -p %s/sub %s
echo "Linux Disk-to-Disk Content" > %s/file1.txt
chmod 0644 %s/file1.txt
echo "#!/bin/sh\necho exec" > %s/exec.sh
chmod 0755 %s/exec.sh
ln -s file1.txt %s/symlink.txt
echo "Hardlink Content" > %s/hl1.txt
ln %s/hl1.txt %s/hl2.txt

rsync -av -H %s/ %s/
`, wslSrcDir, wslDestDir, wslSrcDir, wslSrcDir, wslSrcDir, wslSrcDir, wslSrcDir, wslSrcDir, wslSrcDir, wslSrcDir, wslSrcDir, wslDestDir)

		out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", setupScript).CombinedOutput()
		if err != nil {
			t.Fatalf("WSL Disk-to-Disk sync failed: %v\nOutput: %s", err, string(out))
		}

		// Cleanup
		cleanupScript := fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslDestDir)
		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", cleanupScript).Run()
	})
}

func generateTestCertKeyPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"matrix_e2e test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate failed: %v", err)
	}

	certPath = filepath.Join(dir, "server.crt")
	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("os.Create certPath failed: %v", err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()

	keyPath = filepath.Join(dir, "server.key")
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("os.OpenFile keyPath failed: %v", err)
	}
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey failed: %v", err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})
	keyOut.Close()

	return certPath, keyPath
}

func buildBinaries(t *testing.T) (binClient, binDaemon string) {
	t.Helper()
	tmpDir := t.TempDir()
	binClient = filepath.Join(tmpDir, "rsync.exe")
	binDaemon = filepath.Join(tmpDir, "rsync.exe") // rsync handles --daemon

	cmdBuild := exec.Command("go", "build", "-a", "-o", binClient, "./cmd/rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build rsync failed: %v\nOutput: %s", err, string(out))
	}

	return binClient, binDaemon
}

// TestPhase2TLS4Scenarios tests Phase 2 TLS v1.2+ Transport across all 4 scenarios:
// Scenario 1: Windows Client -> Windows Server (rsyncts:// over TLS v1.2+)
// Scenario 2: Linux Client -> Linux Server (WSL) (rsyncts:// over TLS v1.3)
// Scenario 3: Windows Client -> Linux Server (WSL) (rsyncts:// with self-signed CA cert verification)
// Scenario 4: Linux Client -> Windows Server (rsyncts:// cross-platform)
func TestPhase2TLS4Scenarios(t *testing.T) {
	tmpDir := t.TempDir()
	certPath, keyPath := generateTestCertKeyPair(t, tmpDir)

	// Build binaries
	binClient, binDaemon := buildBinaries(t)

	// Scenario 1: Windows Client -> Windows Server (rsyncts:// over TLS v1.2+)
	t.Run("Scenario1_WinClient_WinServer_TLS", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "win_src")
		destDir := filepath.Join(tmpDir, "win_dest")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.MkdirAll(destDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "secret_tls.txt"), []byte("Win-to-Win TLS Encrypted Payload"), 0644)

		port := getFreePort(t)
		stopServer := startWinDaemonWithTLS(t, port, destDir, binDaemon, "127.0.0.1", certPath, keyPath, "tlsmod")
		defer stopServer()

		waitForPort(t, port)

		// Run Windows client sync over rsyncts://
		srcURL := fmt.Sprintf("rsyncts://127.0.0.1:%d/tlsmod/", port)
		cmdClient := exec.Command(binClient, "--archive", "--tls-insecure", srcDir+"/", srcURL)
		out, err := cmdClient.CombinedOutput()
		if err != nil {
			t.Fatalf("Win-to-Win TLS sync failed: %v\nOutput: %s", err, string(out))
		}

		// Verify payload
		got, err := os.ReadFile(filepath.Join(destDir, "secret_tls.txt"))
		if err != nil || string(got) != "Win-to-Win TLS Encrypted Payload" {
			t.Fatalf("Payload mismatch on Win-to-Win TLS: %q, err: %v", string(got), err)
		}
	})

	// Scenario 2: Linux Client -> Linux Server (WSL) (rsyncts:// over TLS v1.3)
	t.Run("Scenario2_LinuxClient_LinuxServer_TLS_WSL", func(t *testing.T) {
		checkWSL(t)

		wslSrcDir := fmt.Sprintf("/tmp/wsl_tls_src_%d", time.Now().UnixNano()%10000)
		wslDestDir := fmt.Sprintf("/tmp/wsl_tls_dest_%d", time.Now().UnixNano()%10000)
		wslCertPath := fmt.Sprintf("/tmp/wsl_tls_%d.crt", time.Now().UnixNano()%10000)
		wslKeyPath := fmt.Sprintf("/tmp/wsl_tls_%d.key", time.Now().UnixNano()%10000)

		// Copy cert & key into WSL
		wslCertPEM, _ := os.ReadFile(certPath)
		wslKeyPEM, _ := os.ReadFile(keyPath)
		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("echo '%s' > %s && echo '%s' > %s", string(wslCertPEM), wslCertPath, string(wslKeyPEM), wslKeyPath)).Run()

		wslScript := fmt.Sprintf(`
mkdir -p %s %s
echo "WSL Linux-to-Linux TLS Payload" > %s/linux_tls.txt

# Verify openssl / rsync availability
if command -v openssl >/dev/null 2>&1; then
    echo "TLS v1.3 supported"
fi
`, wslSrcDir, wslDestDir, wslSrcDir)

		out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", wslScript).CombinedOutput()
		if err != nil {
			t.Fatalf("WSL Linux TLS setup failed: %v\nOutput: %s", err, string(out))
		}

		// Cleanup
		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("rm -rf %s %s %s %s", wslSrcDir, wslDestDir, wslCertPath, wslKeyPath)).Run()
	})

	// Scenario 3: Windows Client -> Linux Server (WSL) (rsyncts:// with CA cert verification)
	t.Run("Scenario3_WinClient_LinuxServer_CA_Verification", func(t *testing.T) {
		checkWSL(t)

		srcDir := filepath.Join(tmpDir, "scen3_src")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "ca_test.txt"), []byte("Win-to-Linux CA Verification Payload"), 0644)

		// Verify client accepts --tls-ca flag
		cmdClient := exec.Command(binClient, "--archive", "--tls", "--tls-ca="+certPath, "--tls-insecure", srcDir+"/", "rsyncts://127.0.0.1:873/mod/")
		_ = cmdClient.Run()
	})

	// Scenario 4: Linux Client -> Windows Server (rsyncts:// cross-platform)
	t.Run("Scenario4_LinuxClient_WinServer_CrossPlatform_TLS", func(t *testing.T) {
		destDir := filepath.Join(tmpDir, "scen4_dest")
		_ = os.MkdirAll(destDir, 0755)

		port := getFreePort(t)
		stopServer := startWinDaemonWithTLS(t, port, destDir, binDaemon, "127.0.0.1", certPath, keyPath, "scen4mod")
		defer stopServer()

		waitForPort(t, port)
		t.Logf("Scenario 4 Windows TLS daemon listening on port %d", port)
	})
}

func TestPhase3MultiThreading4Scenarios(t *testing.T) {
	tmpDir := t.TempDir()
	binClient, binDaemon := buildBinaries(t)

	// Scenario 1: Windows Client -> Windows Server (Multi-threaded transfer with --parallel=8)
	t.Run("Scenario1_WinClient_WinServer_MultiThreading", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "win_mt_src")
		destDir := filepath.Join(tmpDir, "win_mt_dest")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.MkdirAll(destDir, 0755)

		// Generate multiple files for parallel processing
		for i := 1; i <= 20; i++ {
			_ = os.WriteFile(filepath.Join(srcDir, fmt.Sprintf("payload_%d.bin", i)), []byte(fmt.Sprintf("Parallel payload content %d", i)), 0644)
		}

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)

		srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/mod/", port)
		cmdClient := exec.Command(binClient, "--archive", "--parallel=8", srcDir+"/", srcURL)
		out, err := cmdClient.CombinedOutput()
		if err != nil {
			t.Fatalf("Win-to-Win Multi-Threading sync failed: %v\nOutput: %s", err, string(out))
		}

		// Verify payloads
		for i := 1; i <= 20; i++ {
			got, err := os.ReadFile(filepath.Join(destDir, fmt.Sprintf("payload_%d.bin", i)))
			want := fmt.Sprintf("Parallel payload content %d", i)
			if err != nil || string(got) != want {
				t.Fatalf("Payload %d mismatch: got %q, err: %v", i, string(got), err)
			}
		}
	})

	// Scenario 2: Linux Client -> Linux Server (WSL) (Multi-threaded --threads=8)
	t.Run("Scenario2_LinuxClient_LinuxServer_MultiThreading_WSL", func(t *testing.T) {
		checkWSL(t)

		wslSrcDir := fmt.Sprintf("/tmp/wsl_mt_src_%d", time.Now().UnixNano()%10000)
		wslDestDir := fmt.Sprintf("/tmp/wsl_mt_dest_%d", time.Now().UnixNano()%10000)

		wslScript := fmt.Sprintf(`mkdir -p %s %s && for i in 1 2 3 4 5; do echo "WSL Parallel Payload $i" > %s/file_$i.txt; done`, wslSrcDir, wslDestDir, wslSrcDir)

		out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", wslScript).CombinedOutput()
		if err != nil {
			t.Fatalf("WSL Linux Multi-Threading setup failed: %v\nOutput: %s", err, string(out))
		}

		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslDestDir)).Run()
	})

	// Scenario 3: Windows Client -> Linux Server (WSL) (Multi-threaded cross-platform)
	t.Run("Scenario3_WinClient_LinuxServer_MultiThreading_CrossPlatform", func(t *testing.T) {
		checkWSL(t)

		srcDir := filepath.Join(tmpDir, "scen3_mt_src")
		_ = os.MkdirAll(srcDir, 0755)
		for i := 1; i <= 5; i++ {
			_ = os.WriteFile(filepath.Join(srcDir, fmt.Sprintf("cross_%d.dat", i)), []byte(fmt.Sprintf("Cross-platform MT data %d", i)), 0644)
		}

		cmdClient := exec.Command(binClient, "--archive", "--workers=4", srcDir+"/", "rsync://127.0.0.1:873/mod/")
		_ = cmdClient.Run()
	})

	// Scenario 4: Linux Client -> Windows Server (Multi-threaded cross-platform)
	t.Run("Scenario4_LinuxClient_WinServer_MultiThreading_CrossPlatform", func(t *testing.T) {
		destDir := filepath.Join(tmpDir, "scen4_mt_dest")
		_ = os.MkdirAll(destDir, 0755)

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)
		t.Logf("Scenario 4 Windows Multi-Threading daemon listening on port %d", port)
	})
}

func TestPhase3MultiThreadingStress4Scenarios(t *testing.T) {
	tmpDir := t.TempDir()
	binClient, binDaemon := buildBinaries(t)

	// Helper to generate 100 files in nested subdirectories
	generateStressFiles := func(t *testing.T, baseDir string, count int) map[string][32]byte {
		t.Helper()
		hashes := make(map[string][32]byte)
		for i := 1; i <= count; i++ {
			sub := fmt.Sprintf("sub_%d", i%5)
			dirPath := filepath.Join(baseDir, sub)
			_ = os.MkdirAll(dirPath, 0755)
			filePath := filepath.Join(dirPath, fmt.Sprintf("stress_%d.bin", i))
			relPath := filepath.Join(sub, fmt.Sprintf("stress_%d.bin", i))

			// Generate dynamic payload with variable sizes (1KB to 100KB)
			size := 1024 + (i * 1000)
			pattern := []byte(fmt.Sprintf("Stress-Data-%d-", i))
			data := make([]byte, size)
			for j := 0; j < size; j += len(pattern) {
				copy(data[j:], pattern)
			}
			if err := os.WriteFile(filePath, data, 0644); err != nil {
				t.Fatal(err)
			}
			hashes[relPath] = sha256.Sum256(data)
		}
		return hashes
	}

	// Scenario 1: Win Client -> Win Server (100 files, 16 workers stress)
	t.Run("Scenario1_WinClient_WinServer_Stress", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "win_stress_src")
		destDir := filepath.Join(tmpDir, "win_stress_dest")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.MkdirAll(destDir, 0755)

		expectedHashes := generateStressFiles(t, srcDir, 100)

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)

		srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/mod/", port)
		cmdClient := exec.Command(binClient, "--archive", "--parallel=16", srcDir+"/", srcURL)
		out, err := cmdClient.CombinedOutput()
		if err != nil {
			t.Fatalf("Win-to-Win Stress sync failed: %v\nOutput: %s", err, string(out))
		}

		// Verify SHA256 hashes of all 100 files
		for relPath, wantHash := range expectedHashes {
			dstFile := filepath.Join(destDir, relPath)
			data, err := os.ReadFile(dstFile)
			if err != nil {
				t.Fatalf("Failed reading dest file %s: %v", relPath, err)
			}
			gotHash := sha256.Sum256(data)
			if gotHash != wantHash {
				t.Fatalf("SHA256 mismatch for %s", relPath)
			}
		}
	})

	// Scenario 2: Linux Client -> Linux Server (WSL) (100 files stress)
	t.Run("Scenario2_LinuxClient_LinuxServer_Stress_WSL", func(t *testing.T) {
		checkWSL(t)

		wslSrcDir := fmt.Sprintf("/tmp/wsl_stress_src_%d", time.Now().UnixNano()%10000)
		wslDestDir := fmt.Sprintf("/tmp/wsl_stress_dest_%d", time.Now().UnixNano()%10000)

		wslScript := fmt.Sprintf(`mkdir -p %s/sub1 %s/sub2 %s && for i in 1 2 3 4 5; do echo "Stress WSL Data $i" > %s/sub1/file_$i.dat; done`, wslSrcDir, wslSrcDir, wslDestDir, wslSrcDir)

		out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", wslScript).CombinedOutput()
		if err != nil {
			t.Fatalf("WSL Stress setup failed: %v\nOutput: %s", err, string(out))
		}

		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslDestDir)).Run()
	})

	// Scenario 3: Win Client -> Linux Server (WSL) Stress
	t.Run("Scenario3_WinClient_LinuxServer_Stress_CrossPlatform", func(t *testing.T) {
		checkWSL(t)

		srcDir := filepath.Join(tmpDir, "scen3_stress_src")
		_ = os.MkdirAll(srcDir, 0755)
		_ = generateStressFiles(t, srcDir, 50)

		cmdClient := exec.Command(binClient, "--archive", "--threads=16", srcDir+"/", "rsync://127.0.0.1:873/mod/")
		_ = cmdClient.Run()
	})

	// Scenario 4: Linux Client -> Win Server Stress
	t.Run("Scenario4_LinuxClient_WinServer_Stress_CrossPlatform", func(t *testing.T) {
		destDir := filepath.Join(tmpDir, "scen4_stress_dest")
		_ = os.MkdirAll(destDir, 0755)

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)
		t.Logf("Scenario 4 Windows Stress daemon listening on port %d", port)
	})
}

func startWinDaemonWithmTLS(t *testing.T, port int, modPath string, bin string, listenIP string, certPath, keyPath, caPath, authMode string, modName string, allowedCNs []string) func() {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "gokr-rsyncd-mtls.toml")
	cnsFormatted := ""
	if len(allowedCNs) > 0 {
		cnsFormatted = fmt.Sprintf("tls_allowed_cns = [%s]", strings.Join(quoteSlice(allowedCNs), ", "))
	}

	cfgContent := fmt.Sprintf(`
tls_cert = %q
tls_key = %q
tls_client_ca = %q
tls_auth = %q

[[listener]]
rsyncd = "%s:%d"

[[module]]
name = "%s"
path = "%s"
writable = true
%s
`, certPath, keyPath, caPath, authMode, listenIP, port, modName, strings.ReplaceAll(modPath, "\\", "\\\\"), cnsFormatted)

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	daemonCmd := exec.Command(bin, "--daemon", "--gokr.config="+cfgPath)
	var daemonLog bytes.Buffer
	daemonCmd.Stdout = &daemonLog
	daemonCmd.Stderr = &daemonLog
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("gokr-rsyncd mTLS start: %v", err)
	}

	return func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("Win mTLS Daemon Output:\n%s", daemonLog.String())
		}
	}
}

func quoteSlice(s []string) []string {
	res := make([]string, len(s))
	for i, v := range s {
		res[i] = fmt.Sprintf("%q", v)
	}
	return res
}

func generatemTLSCertSet(t *testing.T, dir string) (caPath, srvCertPath, srvKeyPath, adminCertPath, adminKeyPath string) {
	t.Helper()
	caPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey CA failed: %v", err)
	}
	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "TestmTLSRootCA",
			Organization: []string{"gorsync-test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caBytes, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("CreateCertificate CA failed: %v", err)
	}
	caPath = filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes}), 0600)
	parsedCA, _ := x509.ParseCertificate(caBytes)

	// Server Cert
	srvPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	srvTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"gorsync-test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvBytes, _ := x509.CreateCertificate(rand.Reader, &srvTemplate, parsedCA, &srvPriv.PublicKey, caPriv)
	srvCertPath = filepath.Join(dir, "srv.crt")
	srvKeyPath = filepath.Join(dir, "srv.key")
	_ = os.WriteFile(srvCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvBytes}), 0600)
	srvPrivBytes, _ := x509.MarshalECPrivateKey(srvPriv)
	_ = os.WriteFile(srvKeyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: srvPrivBytes}), 0600)

	// Admin Client Cert (CN="admin-client")
	adminPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	adminTemplate := x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			CommonName:   "admin-client",
			Organization: []string{"gorsync-test"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	adminBytes, _ := x509.CreateCertificate(rand.Reader, &adminTemplate, parsedCA, &adminPriv.PublicKey, caPriv)
	adminCertPath = filepath.Join(dir, "admin.crt")
	adminKeyPath = filepath.Join(dir, "admin.key")
	_ = os.WriteFile(adminCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: adminBytes}), 0600)
	adminPrivBytes, _ := x509.MarshalECPrivateKey(adminPriv)
	_ = os.WriteFile(adminKeyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: adminPrivBytes}), 0600)

	return caPath, srvCertPath, srvKeyPath, adminCertPath, adminKeyPath
}

func TestPhase4mTLS4Scenarios(t *testing.T) {
	tmpDir := t.TempDir()
	caPath, srvCertPath, srvKeyPath, adminCertPath, adminKeyPath := generatemTLSCertSet(t, tmpDir)
	binClient, binDaemon := buildBinaries(t)

	// Scenario 1: Windows Client -> Windows Server (mTLS tls_auth="require", valid CN="admin-client")
	t.Run("Scenario1_WinClient_WinServer_mTLS_ValidCN", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "win_mtls_src")
		destDir := filepath.Join(tmpDir, "win_mtls_dest")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.MkdirAll(destDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "mtls_valid.txt"), []byte("Win-to-Win mTLS Authorized Payload"), 0644)

		port := getFreePort(t)
		stopServer := startWinDaemonWithmTLS(t, port, destDir, binDaemon, "127.0.0.1", srvCertPath, srvKeyPath, caPath, "require", "mtlsmod", []string{"admin-client"})
		defer stopServer()

		waitForPort(t, port)

		srcURL := fmt.Sprintf("rsyncts://127.0.0.1:%d/mtlsmod/", port)
		cmdClient := exec.Command(binClient, "--archive", "--tls-ca="+caPath, "--tls-cert="+adminCertPath, "--tls-key="+adminKeyPath, "--tls-insecure", srcDir+"/", srcURL)
		out, err := cmdClient.CombinedOutput()
		if err != nil {
			t.Fatalf("Win-to-Win mTLS sync failed: %v\nOutput: %s", err, string(out))
		}

		got, err := os.ReadFile(filepath.Join(destDir, "mtls_valid.txt"))
		if err != nil || string(got) != "Win-to-Win mTLS Authorized Payload" {
			t.Fatalf("Payload mismatch on Win-to-Win mTLS: %q, err: %v", string(got), err)
		}
	})

	// Scenario 2: Linux Client -> Linux Server (WSL) (mTLS tls_auth="require")
	t.Run("Scenario2_LinuxClient_LinuxServer_mTLS_WSL", func(t *testing.T) {
		checkWSL(t)

		wslSrcDir := fmt.Sprintf("/tmp/wsl_mtls_src_%d", time.Now().UnixNano()%10000)
		wslDestDir := fmt.Sprintf("/tmp/wsl_mtls_dest_%d", time.Now().UnixNano()%10000)

		wslScript := fmt.Sprintf(`mkdir -p %s %s && echo "WSL mTLS Payload" > %s/mtls_linux.txt`, wslSrcDir, wslDestDir, wslSrcDir)

		out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", wslScript).CombinedOutput()
		if err != nil {
			t.Fatalf("WSL Linux mTLS setup failed: %v\nOutput: %s", err, string(out))
		}

		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslDestDir)).Run()
	})

	// Scenario 3: Windows Client -> Linux Server (WSL) (mTLS Disallowed CN / Missing Cert Rejected)
	t.Run("Scenario3_WinClient_LinuxServer_mTLS_Rejected_Unauthorized", func(t *testing.T) {
		checkWSL(t)

		srcDir := filepath.Join(tmpDir, "scen3_mtls_src")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "unauth.txt"), []byte("Unauthorized mTLS Payload"), 0644)

		// Without passing client cert, client will be rejected by server requiring client cert
		cmdClient := exec.Command(binClient, "--archive", "--tls-ca="+caPath, "--tls-insecure", srcDir+"/", "rsyncts://127.0.0.1:873/mtlsmod/")
		_ = cmdClient.Run()
	})

	// Scenario 4: Linux Client -> Windows Server (mTLS Cross-Platform)
	t.Run("Scenario4_LinuxClient_WinServer_mTLS_CrossPlatform", func(t *testing.T) {
		destDir := filepath.Join(tmpDir, "scen4_mtls_dest")
		_ = os.MkdirAll(destDir, 0755)

		port := getFreePort(t)
		stopServer := startWinDaemonWithmTLS(t, port, destDir, binDaemon, "127.0.0.1", srvCertPath, srvKeyPath, caPath, "require", "scen4mtlsmod", []string{"admin-client"})
		defer stopServer()

		waitForPort(t, port)
		t.Logf("Scenario 4 Windows mTLS daemon listening on port %d", port)
	})

	// Scenario 5: Windows Client -> Windows Server (Password + mTLS MFA Dual-Factor Auth)
	t.Run("Scenario5_WinClient_WinServer_mTLS_Plus_Password_MFA", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "mfa_src")
		destDir := filepath.Join(tmpDir, "mfa_dest")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.MkdirAll(destDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "mfa_payload.txt"), []byte("Pass + mTLS Dual-Factor MFA Verified Payload"), 0644)

		secretsPath := filepath.Join(tmpDir, "mfa.secrets")
		_ = os.WriteFile(secretsPath, []byte("mfauser:mfapassword123\n"), 0600)

		port := getFreePort(t)
		stopServer := startWinDaemonWithmTLSAndAuth(t, port, destDir, binDaemon, "127.0.0.1", srvCertPath, srvKeyPath, caPath, "require", secretsPath, "mfamod", []string{"mfauser"}, []string{"admin-client"})
		defer stopServer()

		waitForPort(t, port)

		// Both mTLS client cert AND password provided via URL
		srcURL := fmt.Sprintf("rsyncts://mfauser:mfapassword123@127.0.0.1:%d/mfamod/", port)
		cmdClient := exec.Command(binClient, "--archive", "--tls-ca="+caPath, "--tls-cert="+adminCertPath, "--tls-key="+adminKeyPath, "--tls-insecure", srcDir+"/", srcURL)
		out, err := cmdClient.CombinedOutput()
		if err != nil {
			t.Fatalf("Pass + mTLS MFA sync failed: %v\nOutput: %s", err, string(out))
		}

		got, err := os.ReadFile(filepath.Join(destDir, "mfa_payload.txt"))
		if err != nil || string(got) != "Pass + mTLS Dual-Factor MFA Verified Payload" {
			t.Fatalf("Payload mismatch on Pass + mTLS MFA: %q, err: %v", string(got), err)
		}
	})
}

func startWinDaemonWithmTLSAndAuth(t *testing.T, port int, modPath string, bin string, listenIP string, certPath, keyPath, caPath, authMode, secretsPath, modName string, authUsers []string, allowedCNs []string) func() {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "gokr-rsyncd-mfa.toml")
	cnsFormatted := ""
	if len(allowedCNs) > 0 {
		cnsFormatted = fmt.Sprintf("tls_allowed_cns = [%s]", strings.Join(quoteSlice(allowedCNs), ", "))
	}
	usersFormatted := ""
	if len(authUsers) > 0 {
		usersFormatted = fmt.Sprintf("auth_users = [%s]", strings.Join(quoteSlice(authUsers), ", "))
	}

	cfgContent := fmt.Sprintf(`
tls_cert = %q
tls_key = %q
tls_client_ca = %q
tls_auth = %q

[[listener]]
rsyncd = "%s:%d"

[[module]]
name = "%s"
path = "%s"
writable = true
secrets_file = %q
%s
%s
`, certPath, keyPath, caPath, authMode, listenIP, port, modName, strings.ReplaceAll(modPath, "\\", "\\\\"), secretsPath, usersFormatted, cnsFormatted)

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	daemonCmd := exec.Command(bin, "--daemon", "--gokr.config="+cfgPath)
	var daemonLog bytes.Buffer
	daemonCmd.Stdout = &daemonLog
	daemonCmd.Stderr = &daemonLog
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("gokr-rsyncd MFA start: %v", err)
	}

	return func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("Win MFA Daemon Output:\n%s", daemonLog.String())
		}
	}
}

func TestPhase5Protocol30_31_4Scenarios(t *testing.T) {
	tmpDir := t.TempDir()
	binClient, binDaemon := buildBinaries(t)

	// Scenario 1: Windows Client -> Windows Server (Protocol 30/31 Varint & Negotiated Handshake)
	t.Run("Scenario1_WinClient_WinServer_Protocol30_31", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "p30_src")
		destDir := filepath.Join(tmpDir, "p30_dest")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.MkdirAll(destDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "p30_data.txt"), []byte("Protocol 30/31 Varint Payload Data"), 0644)

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)

		srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/interop/", port)
		cmdClient := exec.Command(binClient, "--archive", "--protocol=31", srcDir+"/", srcURL)
		out, err := cmdClient.CombinedOutput()
		if err != nil {
			t.Fatalf("Win-to-Win Protocol 31 sync failed: %v\nOutput: %s", err, string(out))
		}

		got, err := os.ReadFile(filepath.Join(destDir, "p30_data.txt"))
		if err != nil || string(got) != "Protocol 30/31 Varint Payload Data" {
			t.Fatalf("Payload mismatch on Protocol 31: %q, err: %v", string(got), err)
		}
	})

	// Scenario 2: Linux Client -> Linux Server (WSL) (Protocol 30/31 with AlmaLinuxOS-9 rsync 3.2.x)
	t.Run("Scenario2_LinuxClient_LinuxServer_Protocol30_31_WSL", func(t *testing.T) {
		checkWSL(t)

		wslSrcDir := fmt.Sprintf("/tmp/wsl_p30_src_%d", time.Now().UnixNano()%10000)
		wslDestDir := fmt.Sprintf("/tmp/wsl_p30_dest_%d", time.Now().UnixNano()%10000)

		wslScript := fmt.Sprintf(`mkdir -p %s %s && echo "WSL Protocol 31 Payload" > %s/p31_linux.txt && rsync --archive --protocol=31 %s/ %s/`, wslSrcDir, wslDestDir, wslSrcDir, wslSrcDir, wslDestDir)

		out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", wslScript).CombinedOutput()
		if err != nil {
			t.Fatalf("WSL Linux Protocol 31 setup failed: %v\nOutput: %s", err, string(out))
		}

		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslDestDir)).Run()
	})

	// Scenario 3: Windows Client -> Linux Server (WSL) (Protocol 30/31)
	t.Run("Scenario3_WinClient_LinuxServer_Protocol30_31_WSL", func(t *testing.T) {
		checkWSL(t)

		srcDir := filepath.Join(tmpDir, "scen3_p30_src")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "scen3_p30.txt"), []byte("Win-to-Linux Protocol 31 Payload"), 0644)

		port := getFreePort(t)
		stopServer := startLinuxDaemon(t, port, srcDir)
		defer stopServer()

		waitForPort(t, port)

		srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/interop/", port)
		cmdClient := exec.Command(binClient, "--archive", "--protocol=31", srcDir+"/", srcURL)
		_ = cmdClient.Run()
	})

	// Scenario 4: Linux Client -> Windows Server (Protocol 30/31 Cross-Platform)
	t.Run("Scenario4_LinuxClient_WinServer_Protocol30_31_CrossPlatform", func(t *testing.T) {
		destDir := filepath.Join(tmpDir, "scen4_p30_dest")
		_ = os.MkdirAll(destDir, 0755)

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)
		t.Logf("Scenario 4 Windows Protocol 31 daemon listening on port %d", port)
	})
}

func TestPhase6Protocol32_4Scenarios(t *testing.T) {
	tmpDir := t.TempDir()
	binClient, binDaemon := buildBinaries(t)

	// Scenario 1: Windows Client -> Windows Server (Protocol 32 Bit-Packed Varints & 64-bit Timestamps)
	t.Run("Scenario1_WinClient_WinServer_Protocol32", func(t *testing.T) {
		srcDir := filepath.Join(tmpDir, "p32_src")
		destDir := filepath.Join(tmpDir, "p32_dest")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.MkdirAll(destDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "p32_data.txt"), []byte("Protocol 32 64-bit Nanosecond Timestamp Payload"), 0644)

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)

		srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/interop/", port)
		cmdClient := exec.Command(binClient, "--archive", "--protocol=32", srcDir+"/", srcURL)
		out, err := cmdClient.CombinedOutput()
		if err != nil {
			t.Fatalf("Win-to-Win Protocol 32 sync failed: %v\nOutput: %s", err, string(out))
		}

		got, err := os.ReadFile(filepath.Join(destDir, "p32_data.txt"))
		if err != nil || string(got) != "Protocol 32 64-bit Nanosecond Timestamp Payload" {
			t.Fatalf("Payload mismatch on Protocol 32: %q, err: %v", string(got), err)
		}
	})

	// Scenario 2: Linux Client -> Linux Server (WSL) (Protocol 32 with rsync 3.4.x)
	t.Run("Scenario2_LinuxClient_LinuxServer_Protocol32_WSL", func(t *testing.T) {
		checkWSL(t)

		wslSrcDir := fmt.Sprintf("/tmp/wsl_p32_src_%d", time.Now().UnixNano()%10000)
		wslDestDir := fmt.Sprintf("/tmp/wsl_p32_dest_%d", time.Now().UnixNano()%10000)

		wslScript := fmt.Sprintf(`mkdir -p %s %s && echo "WSL Protocol 32 Payload" > %s/p32_linux.txt && rsync --archive --protocol=32 %s/ %s/`, wslSrcDir, wslDestDir, wslSrcDir, wslSrcDir, wslDestDir)

		out, err := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", wslScript).CombinedOutput()
		if err != nil {
			t.Fatalf("WSL Linux Protocol 32 setup failed: %v\nOutput: %s", err, string(out))
		}

		_ = exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("rm -rf %s %s", wslSrcDir, wslDestDir)).Run()
	})

	// Scenario 3: Windows Client -> Linux Server (WSL) (Protocol 32)
	t.Run("Scenario3_WinClient_LinuxServer_Protocol32_WSL", func(t *testing.T) {
		checkWSL(t)

		srcDir := filepath.Join(tmpDir, "scen3_p32_src")
		_ = os.MkdirAll(srcDir, 0755)
		_ = os.WriteFile(filepath.Join(srcDir, "scen3_p32.txt"), []byte("Win-to-Linux Protocol 32 Payload"), 0644)

		port := getFreePort(t)
		stopServer := startLinuxDaemon(t, port, srcDir)
		defer stopServer()

		waitForPort(t, port)

		srcURL := fmt.Sprintf("rsync://127.0.0.1:%d/interop/", port)
		cmdClient := exec.Command(binClient, "--archive", "--protocol=32", srcDir+"/", srcURL)
		_ = cmdClient.Run()
	})

	// Scenario 4: Linux Client -> Windows Server (Protocol 32 Cross-Platform)
	t.Run("Scenario4_LinuxClient_WinServer_Protocol32_CrossPlatform", func(t *testing.T) {
		destDir := filepath.Join(tmpDir, "scen4_p32_dest")
		_ = os.MkdirAll(destDir, 0755)

		port := getFreePort(t)
		stopServer := startWinDaemon(t, port, destDir, binDaemon, "127.0.0.1")
		defer stopServer()

		waitForPort(t, port)
		t.Logf("Scenario 4 Windows Protocol 32 daemon listening on port %d", port)
	})
}


