# Repository Guidelines

## Project Structure & Module Organization
- `main.go` contains the full TUI MP3 player implementation (Bubble Tea + Beep).
- `go.mod`/`go.sum` define the Go module (`fase/audioplayer`) and dependencies.
- `flake.nix`/`flake.lock` provide a Nix dev shell with Go tooling and audio deps.
- Sample media lives in the repo root (e.g., `*.mp3`); no separate assets folder.
- There is currently no dedicated `tests/` directory.

## Build, Test, and Development Commands
- `go run .` runs the TUI player in the current directory.
- `go run . -dir /path/to/mp3s` scans a specific folder for `.mp3` files.
- `go build ./...` builds the binary (useful for CI or release artifacts).
- `go test ./...` runs tests (none exist today; expect “no test files”).
- `nix develop` enters the dev shell with Go, ALSA, and lint tools installed.

## Coding Style & Naming Conventions
- Format Go code with `gofmt` (tabs for indentation, standard Go style).
- Use `camelCase` for local variables/functions and `PascalCase` for types.
- Keep UI strings concise and TUI-focused; prefer constants for repeated values.

## Testing Guidelines
- No test framework is set up yet. If you add tests, use the standard Go `testing`
  package and name files `*_test.go`.
- Keep test data small; avoid committing large audio files.

## Commit & Pull Request Guidelines
- Commit messages in history are short and lowercase (e.g., “basic mp player”).
  Follow that style: concise, present-tense, no trailing period.
- PRs should include a brief description of behavior changes and note any
  dependencies (e.g., ALSA requirements or Nix shell usage).
- Include terminal screenshots or GIFs for UI changes when feasible.

## Configuration & Environment Notes
- Playback relies on system audio libraries (ALSA on Linux via `oto`/`beep`).
- If audio initialization fails, verify OS audio setup before changing code.
