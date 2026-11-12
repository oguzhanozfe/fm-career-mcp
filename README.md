# FM Career MCP

A local [Model Context Protocol](https://modelcontextprotocol.io/) server for Football Manager. Connect it to an MCP client to review played matches, scout players and staff, draft tactics and make selected changes to FM27.

## Install

Download the server for your platform from [Releases](https://github.com/oguzhanozfe/fm-career-mcp/releases), or build it with Go 1.26.5 or later.

```sh
go install github.com/oguzhanozfe/fm-career-mcp@v0.2.0
```

For direct `.fm` access, install the `fm_save_parser` executable from [fm-open-scout](https://github.com/YanivGabay/fm-open-scout/releases). Supply its absolute path or put it on PATH. FM24, FM26 and FM27 support depends on that parser. FM27 was exercised with real career files on macOS.

## Connect to your career

Save your career in FM, then add this server to your MCP client's local stdio configuration. Replace the example paths with your own.

```json
{
  "mcpServers": {
    "fm-career": {
      "command": "/absolute/path/to/fm-career-mcp",
      "args": [
        "-save", "/absolute/path/to/My Career.fm",
        "-parser", "/absolute/path/to/fm_save_parser"
      ]
    }
  }
}
```

A TOML-based MCP client can use:

```toml
[mcp_servers.fm_career]
command = "/absolute/path/to/fm-career-mcp"
args = ["-save", "/absolute/path/to/My Career.fm", "-parser", "/absolute/path/to/fm_save_parser"]
```

Optional inputs:

- Use `-players-json /path/to/players.json` in place of `-save` to read a snapshot previously generated with `fm_save_parser players "My Career.fm" --top 0 --json players.json`.
- Use `-staff-json /path/to/staff.json` with a Staff → Export → JSON export from [FMLibreEditor](https://github.com/tozmoz/FMLibreEditor). Include the entire staff group you want to audit. Staff-only mode works by providing just this flag.

The server loads a snapshot on startup. After you save the game or re-export staff, ask your client to call `reload_career`. Save club identifiers come from `find_clubs`. For live tools, obtain the real club UID separately from the FMLibreEditor club profile.

## Analysis tools

| Tool | Result |
| --- | --- |
| `career_info` | FM edition, snapshot dates, counts and parser warnings |
| `reload_career` | Refresh the configured inputs |
| `find_clubs` | Look up club identifiers by name |
| `squad_review` | Positional depth, loans and an optional shortlist with strict CA **and** PA cutoffs |
| `scout_players` | Filter by position, age, ability, wage and an unverified stored value |
| `build_tactic` | In-possession and out-of-possession shapes, team instructions, an XI with no duplicate players and advice on weak positions |
| `staff_audit` | Staff attributes and vacancies against the headcounts you supply |
| `review_match` | Played match analysis with the evidence, suggested changes and what to measure next |
| `search_staff_candidates` | Rank staff by the relevant coaching, recruitment or medical attributes |
| `draft_recruitment_offer` | Draft a transfer or staff contract and check it against the limits you supply |

Try “Build a high press for my club and find young replacements for the positions where the XI has weak fits” or “Show owned players with CA below 130 and PA below 145.”

Squad depth counts familiarity of at least 15 out of 20. The proposed XI uses position familiarity and CA. Check fitness, suspensions, registration and role suitability in FM. Scouting figures marked unverified have uncertain meaning. The staff export supplies employees and roles, while your club's Staff screen supplies the allowed headcounts. Export filters and matching club names can affect the staff totals.

## Assistant-coach workflows

**Review the last match.** Add `-fmsave-cli /path/to/fmsave` alongside `-save`. Install [fmsave](https://github.com/rhiever/fmsave) as a separate CLI. The server reads the managed club's latest played first-team fixture by default. If the save has both teams' match stats, it uses them for chance-quality analysis. On FM27 builds where team stats cannot be decoded, it uses readable player ratings and passing stats. You can also supply both teams' xG, shots and possession from the FM match screen directly to `review_match`. A score alone does not establish a tactical cause.

**Recruit players and staff.** Use `scout_players` for players and `search_staff_candidates` for staff. For outside staff, export an unfiltered staff list from FMLibreEditor. The staff score is an average of role-relevant attributes. It does not establish a candidate's interest or wage demand. Pass an explicit fee, wage, duration and any applicable budgets to `draft_recruitment_offer` for a draft you can enter in FM. The draft does not submit a transfer bid or staff employment offer.

### Live FootballManagerMCP integration

On Windows Steam, set up the API and game plugin from [FootballManagerMCP](https://github.com/FootballManagerMCP/FootballManagerMCP). FM Career MCP connects directly to the API on local port 5120 by default. Use the Windows server binary on the same PC as FM.

```text
-game-api-port 5120
```

This adds live match detail (goals, substitutions, lineup and player stats), current tactics, squad, scouting budget, player scout reports and the in-game database search for player and staff names. With the tactics screen opened once, `live_tactic_options` provides the game's valid keys. You can use just the live integration or combine it with a save snapshot.

Add `-game-club "Exact team name from the live API"` to enable the following:

| Tool | Action |
| --- | --- |
| `preview_live_tactic` | Validate changes to mentality, team instructions, formations, player slots, roles or bench |
| `preview_scouting_action` | Review a scouting assignment or shortlist addition |
| `preview_trial_offer` | Review an in-game player trial invitation of 1–4 weeks |
| `apply_live_action` | Apply the confirmed preview and report failed or skipped steps |

The live bridge uses game IDs from `live_squad` or `find_live_recruits`. Saved player IDs may differ. Changing a tactic slot can make it active, and lineup changes affect the team's shared selection. Review the preview before applying. A trial invitation is separate from a transfer or employment offer. Save in FM afterward if you want the changes to persist.

The integration was tested against the upstream HTTP response shapes with a local fixture server. Actual Windows gameplay was not part of the release test. On macOS, match review and recruitment drafts work from local data. Native tactical changes can be exported with the template workflow below.

## Optional write tools

Write tools appear only when configured. They are intended for local single-player careers. The preview/apply flows keep a short-lived, single-use confirmation token. Review the affected players in your client before applying the change.

### Native tactic variants

Install [FM Tactics Creator](https://github.com/jamespheffernan/fm-tactics-creator/releases/tag/v0.1.2). Add these arguments:

```text
-tactics-cli /path/to/fm-tactics -tactic-template /path/to/YourTemplate.fmf -output-dir /path/to/exports
```

`export_tactic` writes a new `.fmf` from a template you exported from FM. It supports name, mentality and tackling. The template supplies formations, roles and other instructions. Use `build_tactic` for a full plan you can enter in-game, then save that plan in FM if you want a native file. Load the generated FMF from the Tactics screen.

### Player holiday return

Add `-holiday-copies -output-dir /path/to/exports` alongside `-save`. Call `preview_holiday_return` with the club identifier and the desired name for a new save, then `apply_holiday_return` with the preview token. Load the resulting copy in FM. The original save is preserved. Use an existing local output directory that supports hard links.

This experimental writer targets the FM27 2700 database with schema 9 and the verified single-entry active holiday format. It skips loaned players and rejects unexpected active records. It checks that the source is identical to the preview, reopens the generated save with the parser and compares the player data before publishing the copy. Other holiday record layouts and game versions need their own fixtures.

### Heal active injuries

On macOS Steam, use the bridge installed through [FMLibreEditor](https://github.com/tozmoz/FMLibreEditor/blob/main/docs/INSTALL.md) and start FM from Steam. On Windows, the adapter uses the live access supported by FMLibreEditor; the Windows path has not been tested here. Build the adapter with Rust 1.90 or later, or use a release binary when available.

```sh
cargo build --locked --release --manifest-path live/Cargo.toml
```

Add `-live-editor /path/to/fm-career-live -live-pid 12345 -live-club 1234` using the running FM PID and the club UID from FMLibreEditor. Restart the server with the new PID if FM restarts. `preview_heal` lists current injuries for owned players present at that human-managed club. `apply_heal` rechecks the career and the preview, applies the edits and verifies the injured players. Save the career in FM to retain the changes.

The adapter requires the bridge's structural checks and a readable loaded-save counter. If a recently updated build is unprofiled, you can explicitly add `-live-allow-unknown-build` after backing up the career. The same structural checks still apply.

## Steam Workshop

[FM27's Workshop](https://steamcommunity.com/app/4530370/workshop/) currently lists **Tactics** and **League & Data Updates**. The MCP server and live adapter are desktop executables distributed on GitHub. To share a tactic you own, load it in FM27 and use the game's Workshop sharing workflow. This repository contains no Steam upload automation or third-party tactic templates.

## Design and next steps

The MCP server uses local stdio and offers tools based on the inputs and adapters configured at startup. Snapshots drive reports and advice. The FMF writer and live editor run as separate programs with fixed arguments. Holiday edits work on a copy of the selected save. No model credentials or Steam credentials are required by the server. If your client uses a cloud model, it may send tool responses to that model provider.

Further live work includes submitting and tracking normal transfer bids and employment offers, plus a macOS equivalent of the Windows match and tactics bridge. These require game actions that can be verified on the target version and save.

## Development

```sh
go test ./...
go vet ./...
go build ./...
```

The test suite builds its own data. Keep real careers, player exports, FMF templates and client credentials out of the repository.

GPL-3.0-or-later. Independent community project. Football Manager is a trademark of Sports Interactive. Built with the [official MCP Go SDK](https://go.sdk.modelcontextprotocol.io/), [fm-open-scout](https://github.com/YanivGabay/fm-open-scout) for save parsing, [FMLibreEditor](https://github.com/tozmoz/FMLibreEditor) for live injuries and [FM Tactics Creator](https://github.com/jamespheffernan/fm-tactics-creator) for native tactic variants. Archive format reference: [fm-open-scout FORMAT.md](https://github.com/YanivGabay/fm-open-scout/blob/main/FORMAT.md).
