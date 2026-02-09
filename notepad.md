# notepad

## 2026-02-09: Homebrew upgrade still showed 0.1.2 after v0.1.3 release

- Symptom: `brew upgrade hazel-cli` reported "already up to date" at `0.1.2` even though GitHub Actions showed `v0.1.3` released.
- Root cause: local Homebrew tap checkout (`/opt/homebrew/Library/Taps/flip-z/homebrew-hazel`) had not been updated, so the installed formula was still pinned at `version "0.1.2"`.
  - After running `brew update`, the tap moved to the commit "Brew formula update for hazel version v0.1.3" and `brew info hazel-cli` reflected `stable 0.1.3`.
- Fix/workaround:
  - Run `brew update` (or ensure auto-update isn't disabled), then `brew upgrade hazel-cli`.
  - If users have `HOMEBREW_NO_AUTO_UPDATE=1`, they must `brew update` explicitly.

## 2026-02-09: Scheduler flag was awkward; moved to config/UI control

- User correction: having to run `hazel up --scheduler` is awkward; scheduler should be controllable via config/UI so you do not have to return to CLI.
- Change applied:
  - Added `.hazel/config.yaml` `scheduler_enabled: true|false` (default false).
  - `hazel up` always starts the scheduler loop, but it only ticks when `scheduler_enabled` is true and `run_interval_seconds > 0`.
  - Board UI Schedule menu now toggles `scheduler_enabled` and edits `run_interval_seconds`; changes apply without restart.
- Note: current scheduler is interval-based (every N seconds). A wall-clock daily schedule (needs a real time picker) is a separate feature.
