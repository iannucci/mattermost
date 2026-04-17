# Mattermost Post History Limit Removal

## Background

Mattermost introduced a message history limit for Entry-tier licenses that blocks
access to older messages and displays nagware banners. This patch removes that
restriction for our self-hosted emergency services server while keeping the rest
of the codebase intact.

## What the patch does

Three files are modified on the `patches/no-history-limit` branch (based on `v11.6.0`):

| File | Change |
|---|---|
| `server/channels/app/limits.go` | `GetPostHistoryLimit()` always returns 0; `GetServerLimits()` skips post history limit block |
| `server/channels/app/post_helpers.go` | `filterInaccessiblePosts()` and `getFilteredAccessiblePosts()` short-circuit (return immediately) |
| `server/channels/jobs/last_accessible_post/worker.go` | Background job that computes the cutoff timestamp is disabled |

The webapp banners auto-disable when the server returns 0 for `postHistoryLimit` and
`lastAccessiblePostTime`, so no frontend changes are needed.

## Production server

- **Host**: pa-chat.local.mesh
- **SSH**: `ssh root@pa-chat.local.mesh`
- **Install path**: `/opt/mattermost` (systemd service `mattermost`, runs as `mattermost:mattermost`)
- **Database**: PostgreSQL 16 (local), database name `mattermost`
- **Upgraded**: 2026-04-17, from v11.1.1 to patched v11.6.0

## Build instructions

The binary must be built on the target server (Linux x86_64), not cross-compiled.

```bash
# On pa-chat.local.mesh as root:
export PATH=$PATH:/usr/local/go/bin
cd /root/mattermost-build/server
make setup-go-work
go build -o bin/mattermost ./cmd/mattermost
```

Go 1.24.x is installed at `/usr/local/go` on the server. The source repo is at
`/root/mattermost-build`, cloned from the fork at `github.com/iannucci/mattermost`.

## Deploying a new build

```bash
systemctl stop mattermost
cp /root/mattermost-build/server/bin/mattermost /opt/mattermost/bin/mattermost
chown mattermost:mattermost /opt/mattermost/bin/mattermost
setcap cap_net_bind_service=+eip /opt/mattermost/bin/mattermost
systemctl start mattermost
```

The `setcap` step is required because the service runs as a non-root user but binds to port 80.

## Upgrading to a new upstream release

When Mattermost publishes a new version (e.g., v11.7.0):

```bash
# On your Mac, in ~/Documents/github/mattermost:
git fetch upstream --tags
git checkout -b patches/no-history-limit-v11.7.0 v11.7.0
git cherry-pick patches/no-history-limit
# Resolve any conflicts (the patch touches 3 files with minimal surface area)
git push origin patches/no-history-limit-v11.7.0

# On pa-chat.local.mesh as root:
cd /root/mattermost-build
git fetch origin
git checkout patches/no-history-limit-v11.7.0
cd server
make setup-go-work
go build -o bin/mattermost ./cmd/mattermost
```

Then follow the deployment steps above, with a database backup first.

## Backup and rollback

Before any upgrade, always:

1. **Back up the database**:
   ```bash
   sudo -u postgres pg_dump mattermost > /root/mattermost-db-backup-YYYYMMDD.sql
   ```

2. **Preserve the install directory**:
   ```bash
   mv /opt/mattermost /opt/mattermost-VERSION-backup
   cp -a /opt/mattermost-VERSION-backup /opt/mattermost
   ```

To roll back:

```bash
systemctl stop mattermost
rm -rf /opt/mattermost
mv /opt/mattermost-VERSION-backup /opt/mattermost
sudo -u postgres psql -c "DROP DATABASE mattermost;"
sudo -u postgres psql -c "CREATE DATABASE mattermost OWNER mattermost;"
sudo -u postgres psql mattermost < /root/mattermost-db-backup-YYYYMMDD.sql
systemctl start mattermost
```

## Current state

- **Backup directory**: `/opt/mattermost-11.1.1-backup` (original v11.1.1 install)
- **Database backup**: `/root/mattermost-db-backup-20260417.sql` (pre-upgrade snapshot)
- **Build source**: `/root/mattermost-build` (clone of fork, `patches/no-history-limit` branch)
- **Fork**: `github.com/iannucci/mattermost`, branch `patches/no-history-limit`
