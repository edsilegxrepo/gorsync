# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.3.6] - Unreleased

### Added
- **Granular Diagnostic Exit Codes & `ExitError`**: Implemented standard `rsync(1)` exit code constants (`0` success, `1` syntax, `2` protocol, `3` file select, `5` start client, `10` socket I/O, `11` file I/O, `20` signal interrupt) and `ExitError` error wrapper with `.Unwrap()` support in `internal/maincmd/maincmd.go`.
- **Phase 7 Codebase TODO Features & Module Descriptions**: Added `Comment` string field to `rsyncd.Module` struct and TOML daemon configuration, formatted module comments in `--list-only` queries, expanded `sh.ChecksumLength` bounds up to 32 bytes for SHA256/xxHash in `types.go`, and added 4-scenario live interop test suite (`TestPhase7TODOFeatures_4Scenarios`).
- **Phase 6 Protocol 32 Extensions**: Implemented Protocol 32 bit-packed varints (`ReadVarInt32`/`WriteVarInt32`), 64-bit nanosecond timestamp precision (`ReadTime64`/`WriteTime64`), `MaxProtocolVersion = 32`, protocol fallback negotiation, and live 4-scenario interop test suite against AlmaLinuxOS-10 `rsync 3.4.x` (`TestPhase6Protocol32_4Scenarios`).
- **Phase 5 Protocol 30/31 Upgrade**: Implemented Protocol 30/31 variable-length integer encoding primitives (`ReadVarInt`/`WriteVarInt`) in `internal/rsyncwire`, dynamic string-based checksum algorithm negotiation (`xxhash`, `sha1`, `md4`, `md5`) in `internal/rsyncchecksum`, `--protocol` CLI flag, and live 4-scenario interop test suite against AlmaLinuxOS-9 `rsync 3.2.x` (`TestPhase5Protocol30_31_4Scenarios`).
- **Phase 4 Mutual TLS (mTLS) Auth Support & X.509 CN RBAC**: Mandatory client certificate validation (`tls_auth = "require"`, `tls_client_ca`), X.509 Common Name (CN) module access control (`tls_allowed_cns`), support for Pass + mTLS Dual-Factor MFA, and 5-scenario E2E interop test suite (`TestPhase4mTLS4Scenarios`).
- **Phase 3 Multi-Threading Performance Engine**: Default worker capacity (`Workers = NumCPU() * 2`), `--parallel`, `--workers`, `--threads` CLI flags. Integrated `sync.Pool` 128KB buffer recycling in `internal/parallel`, async background hashing goroutines, 4MB TCP socket buffer auto-tuning (`SO_RCVBUF`/`SO_SNDBUF`), and high-volume multi-threaded 4-scenario interop stress test suite (`TestPhase3MultiThreadingStress4Scenarios`).
- **Phase 2 TLS v1.2+ Transport Engine**: Native TLS socket encryption (`rsyncts://`, `--tls`, `--tls-ca`, `--tls-cert`, `--tls-key`, `--tls-insecure`). Plain `rsync://` transport remains the default. Supported on both client and daemon server across native Windows and Linux environments (including WSL).
- **Phase 1 `secretprotector` Mandatory Credential Protection**: Integrated `github.com/edsilegxrepo/secretprotector/pkg/libsecsecrets` into [internal/maincmd/auth.go](internal/maincmd/auth.go) and [rsyncd/rsyncd.go](rsyncd/rsyncd.go) via shared [internal/rsyncsec](internal/rsyncsec/sec.go). All client authentication passwords (`--password-file`, `RSYNC_PASSWORD`, URL credentials) and server secrets (`rsyncd.secrets`) are encrypted in RAM using ephemeral AES-256-GCM master keys via `ProtectedSecret`, revealed on-demand via `.Reveal()`, and immediately zeroed out in RAM upon destruction via `.Destroy()`.
- **Module & Cmd Restructuring**: Updated Go module path to `github.com/edsilegxrepo/rsync` and reorganized binary executables into standard Go layout (`cmd/rsync/main.go` and `cmd/rsyncd/main.go`).

### Fixed
- **Socket Listener Leak in Test Server Helper**: Fixed socket listener cleanup in `internal/rsynctest/rsynctest.go` by registering `t.Cleanup(func() { ts.listener.Close() })` for all custom listeners passed via `rsynctest.Listener(ln)`, resolving orphaned listener sockets and background test hangs.

## [v0.3.5] - 2026-08-03

### Added
- **Local Disk-to-Disk E2E Integration Suite**: Added `TestE2ELocalDiskToDisk` in [integration/interop/matrix_e2e_test.go](integration/interop/matrix_e2e_test.go) asserting 100% preservation of executable mode bits (`0755`), modification times (`mtime`), symlinks (`-l`), hardlinks (`-H`), and SHA256 content parity natively on Windows, Linux WSL, and cross-drive mounts.
- **Sender `--delete` & Filter Support**: Added full support for `--delete`, `--delete-excluded`, `--exclude`, `--include`, and `--filter` flags when pushing files as a client sender.
- **Wildcard Pattern Matching**: Implemented `wildmatch` (`*`, `**`, `?`, `[...]` character classes) for rsync filter rules in [internal/sender/exclude.go](internal/sender/exclude.go).
- **Rsync Daemon Protocol Authentication**:
  - Implemented MD4 challenge-response authentication for `rsync://` client connections and `gokr-rsyncd` server modules.
  - Added support for password sources: `rsync://user:pass@host/module` URLs, `--password-file`, and `RSYNC_PASSWORD` environment variable.
  - Added `AuthUsers` and `SecretsFile` fields to `rsyncd.Module` in [rsyncd/rsyncd.go](rsyncd/rsyncd.go).
- **Whole-File Transfer Mode (`-W`)**: Added end-to-end support for `--whole-file`/`-W` flags to bypass rolling checksum generation on fast/local transfers in [internal/receiver/generator.go](internal/receiver/generator.go).
- **Daemon Directory Listing Mode (PR #56)**: Added daemon list-only directory mode (`SetListOnly`) and `-d` (`--dirs`) flag support in [internal/maincmd/clientmaincmd.go](internal/maincmd/clientmaincmd.go) and [internal/rsyncopts/serveroptions.go](internal/rsyncopts/serveroptions.go) for querying remote `rsync://` modules.
- **I/O Timeout & Tarpit Protection (`--timeout`)**: Added `--timeout` socket I/O deadline wrapper (`TimeoutConn`) in [internal/maincmd/clientserver.go](internal/maincmd/clientserver.go) to protect clients and servers against network stalls.
- **Bandwidth Rate Limiting (`--bwlimit`)**: Added token-bucket bandwidth rate limiter (`RateLimiter`) in [internal/rsyncwire/ratelimit.go](internal/rsyncwire/ratelimit.go) to support `--bwlimit=RATE` (in KiB/s) throttling on transfers.
- **Fast Size-Only Mode (`--size-only`)**: Added support for `--size-only` flag to bypass timestamp comparisons and skip transfers whenever file sizes match in [internal/receiver/generator.go](internal/receiver/generator.go).
- **Structured Directory Listing API (Issue #64)**: Added `Client.ListFiles()` and `rsyncclient.File` struct in [rsyncclient/rsyncclient.go](rsyncclient/rsyncclient.go), allowing programmatic retrieval of structured Go file objects from remote rsync daemons without performing disk I/O or parsing text output.
- **Writable Virtual Filesystem Interface (`WritableFS`) (Issue #8)**: Introduced `rsync.WritableFS` interface in [writablefs.go](writablefs.go) and added `WritableFS` support to `rsyncd.Module` and `receiver.TransferOpts`, enabling pure Go in-memory or cloud storage (S3/GCS/Database) upload targets without physical disk access.
- **Staged Temporary Directory (`--temp-dir` / `-T`)**: Added support for `--temp-dir=DIR` (`-T`) in [internal/receiver/receiverrenameio.go](internal/receiver/receiverrenameio.go) to stage partial incoming transfers in an isolated directory.
- **Hardlink Preservation (`-H` / `--hard-links`)**: Added hardlink preservation support (`PreserveHardlinks`) in [rsyncd/rsyncd.go](rsyncd/rsyncd.go) and [internal/maincmd/clientmaincmd.go](internal/maincmd/clientmaincmd.go).
- **Cross-Platform Interoperability Matrix & WSL 2 Integration Suite**: Added end-to-end matrix test suite in [integration/interop/matrix_e2e_test.go](integration/interop/matrix_e2e_test.go) and [integration/interop/wsl_e2e_test.go](integration/interop/wsl_e2e_test.go) validating all 4 dataflow topologies (`Win Client -> Win Server`, `Linux Client -> Linux Server`, `Win Client -> Linux Server`, `Linux Client -> Win Server`) across flags, symlinks, hardlinks, executable modes (`0755`), daemon authentication, chroot sandboxing, and high-volume stress testing.

### Fixed
- **Windows Forward-Slash Drive Letter Parsing Fix**: Fixed drive letter hostspec parsing in [internal/maincmd/options.go](internal/maincmd/options.go) when Windows disk paths use forward slashes (`F:/path` or `C:/path`), preventing local disk paths from being misidentified as remote SSH hosts.
- **Non-Fatal Error Handling**: Fixed multiplex stream handling in [internal/rsyncwire/wire.go](internal/rsyncwire/wire.go) to treat `MsgErrorXfer` (1), `MsgError` (3), `MsgWarning` (4), and `MsgLog` (6) as non-fatal warnings rather than aborting the entire session.
- **Regular File Exclusion Fix**: Fixed file list walking in [internal/sender/flist.go](internal/sender/flist.go) so excluding a regular file skips only that file rather than dropping sibling entries in the parent directory.
- **Windows Subdirectory Receiver Fix**: Normalized subdirectory path separators and trimmed trailing slashes in [rsyncd/rsyncd.go](rsyncd/rsyncd.go), fixing receiver subdirectory creation on Windows.
- **Landlock ACL Named-Directory Fix (Issue #66)**: Automatically included parent directories in `roDirs` for sources without trailing slashes in [internal/maincmd/clientmaincmd.go](internal/maincmd/clientmaincmd.go), preventing `OpenRoot` permission denied errors under Landlock.
- **Daemon Path Jail Enforcement (Issue #48)**: Integrated `os.OpenRoot` jail enforcement in [rsyncd/rsyncd.go](rsyncd/rsyncd.go) to strictly isolate module roots and reject `..` parent directory traversal attempts.
- **Windows Path Normalization Fix (Issue #30)**: Normalized Windows file list paths using `filepath.ToSlash()` in [internal/sender/flist.go](internal/sender/flist.go), preventing absolute Windows paths (`C:\Users\...`) from leaking into remote wire transmissions.
