# tailmount

Share the current directory as a volume that other machines can mount.

`tailmount` is to [tailcat](https://github.com/tailscale/tailcat) what `sshfs`
is to `ssh`. Run it in a directory. It prints an address. Anyone who has the
address can mount that directory on macOS or Linux, even from another network.
No accounts, no tailnet, no kernel extensions.

## How it works

- The sharer runs an NFSv3 server in userspace ([go-nfs](https://github.com/willscott/go-nfs))
  and publishes it through a [tailcat](https://github.com/tailscale/tailcat) tunnel
  (WireGuard, NAT traversal and DERP relays from Tailscale, without the control plane).
- The address is the only secret. It contains the server key and a pre-shared key.
- The client opens the tunnel, forwards a local port into it, and runs the
  operating system's own NFS `mount`.

## Install

```bash
go install github.com/fcsonline/tailmount/cmd/tailmount@latest
```

Linux clients also need the NFS client tools: `nfs-common` (Debian, Ubuntu) or
`nfs-utils` (Fedora, RHEL).

## Use

On the machine that shares:

```bash
cd ~/project
tailmount
```

```
sharing /Users/me/project (rw)
# 🐈 tailmount address: tcpGFwWCB7pg8...
on another machine run:
  tailmount tcpGFwWCB7pg8... <empty directory>
```

On the machine that mounts:

```bash
mkdir ~/project-share
tailmount tcpGFwWCB7pg8... ~/project-share
```

The share appears at `~/project-share`. Press Ctrl-C to unmount.

### Commands

| Command | Effect |
|---|---|
| `tailmount` | Share the current directory, read-write. |
| `tailmount --ro` | Share the current directory, read-only. |
| `tailmount share [dir]` | Share `dir`. Accepts `--ro` and `-v`. |
| `tailmount <addr> <dir>` | Mount a share on `dir`, an empty directory. |
| `tailmount mount <addr> <dir>` | Same as above. |
| `tailmount unmount <dir>` | Unmount a share if the client process is gone. |
| `tailmount version` | Print the version. |

Add `-v` to either side for tunnel and NFS diagnostics.

## Access modes

- `rw` (default): clients can read, write, create, rename and delete.
- `ro`: the server rejects every write. The client also mounts read-only, so
  writes fail with "Read-only file system".

Read-only is enforced on the sharer. A modified client cannot bypass it.

## Platform notes

**macOS.** No `sudo`. The kernel lets you mount onto a directory you own.
The mount uses `locallocks`, `soft` and a 1 MiB transfer size.

**Linux.** `mount` needs root, so tailmount runs `sudo mount` and `sudo umount`
and asks for your password. The mount uses `nolock`, `noacl`, `soft` and a
1 MiB transfer size.

The mountpoint must be an empty directory. If tailmount created the
directory, it removes it again on unmount.

## Limitations

- Stop the sharer and every mount of it becomes stale. Unmount and mount again.
- File ownership shows the sharer's numeric uid and gid. Access checks do not
  depend on it; everything the sharer can do, a client can do.
- Absolute symlinks inside the share resolve on the client's filesystem.
- A Mac client writes `._name` sidecar files (AppleDouble) next to every file
  it copies in, because NFS has no extended attributes, plus `.DS_Store` from
  Finder. The sharer hides both from directory listings, so other clients do
  not see them, but they still exist on the sharer's disk. Run `dot_clean` on
  the shared directory to fold them away. Copies from a Mac keep working
  because the data file is written first. One edge case: a macOS client that
  has just listed a directory answers lookups from that listing, so a hidden
  sidecar of a file that already existed cannot be opened by name until the
  directory changes. On a read-only share Finder shows one "cannot be
  modified" alert per folder.
- Soft mounts: if the tunnel stalls for a long time, an operation can fail
  with an I/O error instead of blocking forever.
- Hard links are not supported.
- Windows is not supported.

## Security

- The address is a bearer secret. Share it over a private channel. Anyone who
  holds it has the same access to the directory as the sharer, for as long as
  the sharer runs.
- Each run creates a new address. Stop the process to revoke access.
- Paths are confined to the shared directory, including through symlinks.
- Public DERP relays are rate-limited and offered without guarantees.
  See the tailcat README for running your own relay.

## Development

```bash
make build   # bin/tailmount
make test    # go test -race ./...
make lint    # gofmt and go vet
```

Layout:

- `cmd/tailmount`: CLI entry point.
- `internal/share`: the sharer (NFS server behind tailcat).
- `internal/mount`: the client (tunnel proxy plus OS mount).
- `internal/tunnel`: adapts tailcat's per-connection callback to `net.Listener`.
- `internal/rofs`: read-only wrapper for the served filesystem.
- `internal/sharefs`: the served filesystem, confined to the shared directory,
  with symlink-safe remove and rename and with chmod, chtimes and no-op chown.

## License

BSD-3-Clause. tailcat and go-nfs are used under their own licenses.
