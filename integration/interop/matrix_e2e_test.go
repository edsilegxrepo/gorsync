//go:build windows

package interop_test

import (
	"bytes"
	"fmt"
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
	binClient := filepath.Join(tmpDir, "gokr-rsync.exe")
	binDaemon := filepath.Join(tmpDir, "gokr-rsync.exe") // gokr-rsync handles --daemon

	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/gokr-rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build gokr-rsync failed: %v\nOutput: %s", err, string(out))
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
	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/gokr-rsync")
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
	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/gokr-rsync")
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
	cmdWSLAuth := exec.Command("wsl.exe", "--cd", "/tmp", "bash", "-c", fmt.Sprintf("export RSYNC_PASSWORD=password123; rsync -av rsync://alice@127.0.0.1:%d/authmod/ %s", port, toWSLPath(destLinuxPass)))
	if out, err := cmdWSLAuth.CombinedOutput(); err != nil {
		t.Fatalf("WSL Authenticated request with RSYNC_PASSWORD failed: %v\nOutput: %s", err, string(out))
	}

	verifyFileEqual(t, testFile, filepath.Join(destLinuxPass, "secret_doc.txt"))
}

func TestE2EStress(t *testing.T) {
	checkWSL(t)

	tmpDir := t.TempDir()
	binClient := filepath.Join(tmpDir, "gokr-rsync.exe")

	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/gokr-rsync")
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

	binClient := filepath.Join(tmpDir, "gokr-rsync.exe")
	cmdBuild := exec.Command("go", "build", "-o", binClient, "./cmd/gokr-rsync")
	cmdBuild.Dir = findRepoRoot(t)
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build gokr-rsync failed: %v\nOutput: %s", err, string(out))
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

