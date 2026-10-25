# Recovery after reboot or network loss

Run the recovery check as root at boot and every five minutes. It handles the
existing Nord exit stack, NAS search/mobile service and protected upgrade worker.
Keep DSM's Tailscale and Container Manager packages enabled at boot.

## Recovery behavior

- Restore `/dev/net/tun` and load the existing `tun` driver when Nord is stopped.
  Refuse an unexpected device, ordinary file or symlink.
- Start Nord, then wait for its existing VPN retries. Start Tailscale only once
  Nord is healthy. A slow first VPN connection does not leave Tailscale stopped.
- Restart the exit-node Tailscale container if the VPN restarted after it, so it
  joins the current network namespace. Require the exact project, service and VPN
  container reference; changed container identities need administrator repair.
- Check that Tailscale reports `Running` and `Online`. This is not a phone egress
  test; verify Australia from the phone after recovery.
- Start stopped search containers, check private search health and mobile OAuth
  metadata, and restart a stopped upgrade worker. A shared deployment lock keeps
  search recovery from fighting phone upgrades. The installer inherits that lock
  so it remains held if its parent worker dies during deployment.

Nord and search each permit at most three automatic container interventions per
rolling hour. Healthy services remain untouched. A failed subsystem does not
skip the others. The next scheduled check retries after an extended outage;
Gluetun and Tailscale retain their own network reconnect behavior. Running but
unhealthy search is reported, rather than repeatedly interrupted.

Recovery does not change accounts, VPN settings, Funnel, images, credentials or
unrelated containers. It cannot reauthenticate expired identities, repair a corrupt
index, or compensate for failing disks. It never loads privileged Compose settings
from the shared `/volume1/docker` folder: it starts or restarts only the existing,
specifically identified containers. Disable recovery tasks before deliberately
stopping these services for maintenance.

## Install the reviewed scripts

Stage `nas-recovery.py`, `nord-exit-recovery.py` and `upgrade-host.py` from
`scripts/` together in a private staging folder. Check their SHA-256 hashes
against the reviewed checkout. For `~/nas-recovery-staging`:

```sh
sha256sum ~/nas-recovery-staging/*.py
sudo /usr/bin/python3 ~/nas-recovery-staging/nas-recovery.py --install
sudo /usr/bin/python3 /volume1/@nas-recovery/nas-recovery.py
```

The installer places recovery code in `/volume1/@nas-recovery`. It updates the
existing root upgrade worker and keeps its first backup as
`upgrade-host.before-recovery.py`. It pauses only the exact expected root worker,
checks process identity, refuses pending/running upgrades, replaces and restarts
the idle worker, and verifies the new coordination protocol. Paused processes
are resumed on failure. A failed install may already have staged new code:
inspect the protected installation before retrying. Never delete deployment
credentials or Tailscale state to retry.

Recovery and upgrade directories require root ownership, trusted ancestors and no
group/world write permissions. Verify DSM ACLs also prevent other users from
writing or deleting these directories. No settings or original source files are
copied. The native NAS Tailscale service and work Tailscale connection stay as they
are.

## Add two DSM tasks

In **Control Panel → Task Scheduler**, create both tasks for user **root**:

1. **Triggered Task → User-defined script**, event **Boot-up**.
2. **Scheduled Task → User-defined script**, daily, repeating every **5 minutes**
   throughout the day.

Use this command for both:

```sh
/usr/bin/python3 /volume1/@nas-recovery/nas-recovery.py
```

Enable abnormal-result notifications if DSM email notifications already work.
Output uses fixed status categories, without Docker logs, settings or credentials.
Recovery writes only small retry-state files and does not append an unbounded log.
Overlapping checks return immediately. The existing upgrade-worker boot task can
remain; the worker's lock prevents duplicate workers.

Disable/delete both tasks to remove recovery. Keep the protected upgrade worker
and deployment credentials. A manually stopped service will otherwise be started
again by recovery.

## Verify

Run both tasks manually. Normal results are worker `running`, search `running`,
and Nord `ready`. A start request or wait can require another scheduled check;
it does not mean access is already restored. Search health and mobile metadata
checks assume deployed loopback ports 8080 and 8082. An installation with OAuth
disabled or different ports needs an adapted check.

After the next planned reboot and after an ordinary network outage, confirm
phone sign-in, document search and MCP `upgrade_status`. Select Nord Australia
on the phone and confirm an Australian public IP. Do not reboot or interrupt
network access solely to test without the owner's approval; other workloads use
the NAS.

Evidence on 2026-10-24: manual recovery established that the tunnel device appeared
after Docker had failed to start Nord. Nord then needed longer than the first
Compose health wait to connect. A second `up` started Tailscale, and native NAS
Tailscale confirmed Nord Australia online. The automated scripts and planned
reboot/network-loss tests are not yet validated on the NAS. These Python
operating-system adapters are tested, not formally proved.
