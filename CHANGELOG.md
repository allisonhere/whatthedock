# Changelog

Notable changes to WhatTheDock. Versions follow the git tags; patch releases that
only re-cut a build are folded into the nearest entry.

## [Unreleased]

### Added
- `l` on a stack row shows the merged, service-tagged logs of every service in
  the stack (filter, pause, follow and copy work as usual).
- Pause/unpause (`Alt+p`) and kill (command palette) for the selected container.
- Dashboard: `s` sorts the list by name, CPU, memory, or network (heaviest
  first), and the header marks the active column. The highlight follows its
  container when a refresh reorders the rows.
- Dashboard: the header is now four stat cards (CPU, MEM, NET, FLEET) with
  heat-tinted borders that pulse on refresh and a segmented status bar; small
  terminals keep the compact summary.
- Dashboard: `g` groups containers under a header per Compose stack, with a
  per-stack rollup of counts, CPU, memory and attention.
- Dashboard: UP and RST columns show each container's uptime and restart count
  (the count is inspected every 30 seconds, not every poll), rows are banded,
  and the selected row is washed with the accent colour.
- Dashboard: the bottom row names the containers that need attention, with the
  reason.
- Dashboard: every Graph style now redraws the CPU, memory and network columns of
  the container list (before, only network and memory changed, and CPU never
  did). `gauge` and `bars` fill a bar with the latest value (thin, or chunky
  with eighth-cell precision); `wave`, `blocks` and `braille` draw the recent
  history of each metric.

### Fixed
- `Alt+r` (restart) never fired: the key handler matched `r` and then checked
  for Alt, but Bubble Tea reports the key as `alt+r`.
- Dashboard: fleet RAM summed every container's limit, so containers without a
  limit (which report the host's total) counted the whole machine once each. It
  now shows the host's capacity once.
- Dashboard: the bottom row said "all healthy" while the header counted an
  UNHEALTHY container. Unhealthy and restarting containers now count as needing
  attention.
- Dashboard: fleet CPU is shown as a share of the whole machine, with the core
  count, instead of a sum of per-core percentages.
- Container CPU% was a lifetime average, not current load: Docker's one-shot
  stats carry no previous sample, so it divided the container's CPU time since
  start by the host's uptime. CPU is now measured against the previous poll of
  the same container (the first poll of a container shows 0%).
- Logs could show garbage or lose their first characters on a line: the
  multiplexed stdout/stderr stream was un-framed by guessing per line, which
  broke whenever a frame header's length byte happened to be a newline. Logs are
  now demultiplexed properly (TTY containers pass through untouched), and a log
  line over 1 MiB is split instead of silently ending the stream.
- The keychain-mode SSH tunnel never recovered after a network drop or
  sleep/wake: the local socket stayed up, so the dead connection was reused until
  the app restarted. It now reconnects on its own (re-reading the password from
  the keychain), and opening a channel on a stalled connection times out.
- The keychain-mode SSH handshake had no timeout when the caller's context had no
  deadline, so a server that accepted the connection and went silent hung the
  app at startup. It is now bounded to 10 seconds.
- Editing, creating, deleting or restoring a Compose file reset its permissions to
  `0644`, making a `0600` file with secrets world-readable, and replaced a
  symlinked file with a regular one. The original mode is now kept (remotely via
  `cp -p`) and writes go through a symlink to its target.

### Changed
- Settings is now a split panel: a section list on the left and the selected
  section's rows on the right. `Esc` leaves the rows first, then closes.
- Graph style `wave` is now a smoothed trend whose older samples fade, in the
  Dashboard and the Stats pane. Its old glyph list was not monotonic, so a bigger
  value could draw shorter than a smaller one.

## [0.1.29] - 2026-10-18

### Fixed
- Stuck "connecting" status, clipped help text, placeholder image, and the
  start/stop label.
- Non-socket paths are preserved; resource curators scroll.

## [0.1.28] - 2026-09-13

### Added
- Restore-last-compose-backup action.
- Secret and recovery hygiene checks in `doctor`.
- Shutdown/reboot of a host now requires typing the host name to confirm.
- End-to-end tests for the dangerous Compose edit seams.

### Fixed
- Paste failures for missing bind mounts and host networking; errors are surfaced.
- System-id collisions, download caps, and compose parsing bugs.
- Update client no longer caps downloads; the signing key matches the baked
  verifier, with a preflight against drift.
- Batch of data-loss bugs; Compose applies are now recoverable.
- Compose edit dirty tracking replaced with a baseline-based model.

## [0.1.27] - 2026-09-13

### Changed
- README and docs refreshed for the current app.

## [0.1.26] - 2026-09-12

### Added
- The UI follows the Omarchy desktop theme, and every widget obeys it.
  `OMARCHY_DIR` overrides the theme-color resolver.

### Changed
- Dashboard highlight appears only after navigation, rail-only.

## [0.1.24] - 2026-09-11

### Added
- `whatthedock doctor` diagnostic command.
- CPU gauge, braille-dot level coloring, and longer stats history with peak labels.

### Fixed
- Compose service edits silently discarding changes or wiping the base file.
- IPv6 port parsing and env whitespace loss in create/edit/paste.
- Published ports duplicated as exposed-only in Container Clipboard yank.
- Unsafe remove-before-create sequence when replicating a standalone container.
- Remote SSH Compose operations hanging or failing on auth.
- `start.sh` silently tunneling to a hardcoded remote host.

## [0.1.23] - 2026-08-28

### Added
- Container Clipboard, real field editing, and host shutdown/reboot.
- Missing bind-mount sources are redirected to a placeholder on paste.
- Copy the full app log from the overlay.
- Command palette organized into labeled categories.
- Dashboard eye candy: mood strip, fleet sparklines, hot-row tint.

### Fixed
- Leaked Compose labels on paste and gutted Adopt YAML.

## [0.1.21] - 2026-08-24

### Added
- Docker image and Compose catalog curation workflows.

### Changed
- Dashboard graphs are color-graded; ANSI codes are stripped from log lines.

## [0.1.17 – 0.1.20] - 2026-08-20 to 2026-08-22

### Added
- Create Stack: a multi-service compose paste deploys whole.
- Delete Stack, mirroring Edit Stack entry points.
- Compose catalog workflow.

### Changed
- Simpler stack tree; the topbar counts only multi-container stacks.
- Inspector mounts are sorted so they stop reshuffling.

## [0.1.8 – 0.1.16] - 2026-08-19

### Added
- Keychain SSH auth mode for non-interactive connections.
- Space toggles live/paused log tailing; live inline log filter; `L` expands logs.
- Install progress on the update popup.

### Fixed
- Password-auth SSH systems no longer hang launch.
- Create failures keep the form instead of losing it.
- Topbar wrapping and multi-line error mangling.

## [0.1.5 – 0.1.7] - 2026-08-17 to 2026-08-18

### Added
- Self-update, AI-assisted problem analysis, app log, and Compose adoption.
- Full-screen fleet Dashboard (`d`), bars and gauge graph styles, and a
  "start in dashboard" setting.

## [0.1.0 – 0.1.4] - 2026-08-13 to 2026-08-16

### Added
- Managed Docker systems, container/Compose creation, Ripple Compose editor.
- Delete, Replicate, Clone, and in-place edit (`m`) for containers and services.
- Exec shell into running containers; SSH tunnel reconnect on stale sockets.
- Status-bar spinner and real pull progress.
- Initial TideDock vertical slice, renamed WhatTheDock.
