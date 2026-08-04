// Package rsync provides a pure Go implementation of the rsync file synchronization
// protocol suite (client CLI, server daemon, and embedded library), fully compatible
// with upstream rsync versions 2.6.x (Protocol 27), 3.0.x-3.3.x (Protocol 30/31),
// and 3.4.x (Protocol 32).
//
// Core Components:
//   - Client CLI (`gorsync`): Local and remote file synchronization tool supporting
//     archive mode (-a), delta transfers, progress monitoring, filters, and mTLS.
//   - Daemon Server (`gorsyncd`): Standalone or systemd socket-activated daemon
//     enforcing OS-level chroot sandboxing (`os.Root` / `pivot_root`).
//   - Virtual Filesystem (`rsync.WritableFS`): Interface abstraction for custom in-memory
//     or cloud storage backends (S3, database, etc.).
//   - Embedded Client (`rsyncclient`): Programmatic Go client for structured file listing
//     and programmatic transfers.
//
// Data Flow:
//
//	Client/Server Handshake -> Protocol Version & Checksum Negotiation -> Challenge-Response
//	Authentication -> File List Exchange -> Delta Sum Generation -> Stream Transmission ->
//	Atomic Temp-File Staging & Renaming.
package rsync
