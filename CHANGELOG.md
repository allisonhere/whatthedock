# Changelog

Notable changes to WhatTheDock. Versions follow the git tags; patch releases that
only re-cut a build are folded into the nearest entry.

## [Unreleased]

### Added
- `l` on a stack row shows the merged, service-tagged logs of every service in
  the stack (filter, pause, follow and copy work as usual).
- Pause/unpause (`Alt+p`) and kill (command palette) for the selected container.

### Fixed
- `Alt+r` (restart) never fired: the key handler matched `r` and then checked
  for Alt, but Bubble Tea reports the key as `alt+r`.

### Changed
- Settings is now a split panel: a section list on the left and the selected
  section's rows on the right. `Esc` leaves the rows first, then closes.

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
