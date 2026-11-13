# FM Career MCP

A local [Model Context Protocol](https://modelcontextprotocol.io/) server for Football Manager. Connect it to an MCP client to review played matches, scout players and staff, draft tactics and make selected changes to FM27.

## Install

Download the server for your platform from [Releases](https://github.com/oguzhanozfe/fm-career-mcp/releases), or build it from source with Go 1.26.5 or later.

```sh
go install github.com/oguzhanozfe/fm-career-mcp@v0.8.0
```

For direct `.fm` access, install the `fm_save_parser` executable from [fm-open-scout](https://github.com/YanivGabay/fm-open-scout/releases). Supply its absolute path or put it on PATH. FM24, FM26 and FM27 support depends on that parser. FM27 was exercised with real career files on macOS.

## Connect to your career

Save your career in FM, then register the same local stdio server in Codex and Claude Code. Replace the example paths with your own. Install [fmsave](https://github.com/rhiever/fmsave) for the next-match and post-match workflows.

```sh
codex mcp add fm-career -- /absolute/path/to/fm-career-mcp \
  -save "/absolute/path/to/My Career.fm" -parser /absolute/path/to/fm_save_parser \
  -fmsave-cli /absolute/path/to/fmsave
claude mcp add --scope user --transport stdio fm-career -- /absolute/path/to/fm-career-mcp \
  -save "/absolute/path/to/My Career.fm" -parser /absolute/path/to/fm_save_parser \
  -fmsave-cli /absolute/path/to/fmsave
```

For [Claude Desktop](https://modelcontextprotocol.io/docs/develop/connect-local-servers), add the entry to `claude_desktop_config.json` and restart the app.

```json
{
  "mcpServers": {
    "fm-career": {
      "command": "/absolute/path/to/fm-career-mcp",
      "args": [
        "-save", "/absolute/path/to/My Career.fm",
        "-parser", "/absolute/path/to/fm_save_parser",
        "-fmsave-cli", "/absolute/path/to/fmsave"
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

The server checks the configured save for changes before using its cached player data and reloads it after a completed write. `career_info` reports the file name, write time and in-game date, and flags the last readable revision if refresh fails. `coach_brief` and `review_match` read a fresh, stable copy for their reports. Use `reload_career` after re-exporting staff or player JSON. When using Save As, update the configured save path in the client. The file workflow reads saves written by FM, including autosaves at the configured path. It cannot issue a Save Game command to FM. Save club identifiers come from `find_clubs`. For live tools, obtain the real club UID separately from the FMLibreEditor club profile.

## Fair mode

Choose `-mode fair` for a read-only assistant coach. It reads a fixed set of manager-visible fields from your saved career, including your first team's fixtures, team match statistics, tactic slots, lineup and training calendar, plus your club's contract expiry dates. Save in FM to refresh the information. Missing reports stay missing, and you can supply the figures from the FM screen.

Fair mode has no player or staff database search, hidden CA/PA or attributes, live bridge, or save-editing tools. For lineup and transfer advice, supply the positions, availability, budget and scouting reports you can see in FM. The assistant can recommend changes for you to enter in the game.

- `player_performance` ranks your saved first-team season numbers by minutes, rating, goals, xG, xA or progressive passing. Per-90 rankings use a 450-minute minimum by default. The result lists the competition scopes with usable records.
- `rank_scout_reports` scores candidates from the visible attributes you provide. Add a reference player to rank similar profiles, and add fee and wage ceilings to screen for affordability. Candidates with unknown required values stay unranked with reasons. All amounts must use the same currency. This tool also works with no save.
- `plan_visible_xi` accepts 11 custom position slots, including wingbacks, and the positions of the eligible players you provide. It places each player at most once and leaves gaps visible.
- `plan_team_training` proposes up to 14 days from the saved fixtures and calendar. It accounts for travel and uses recovery after games, lighter work before games and a chosen tactical focus on open days.

```sh
codex mcp add fm-career -- /absolute/path/to/fm-career-mcp \
  -mode fair -save "/absolute/path/to/My Career.fm" -fmsave-cli /absolute/path/to/fmsave
claude mcp add --scope user --transport stdio fm-career -- /absolute/path/to/fm-career-mcp \
  -mode fair -save "/absolute/path/to/My Career.fm" -fmsave-cli /absolute/path/to/fmsave
```

For Claude Desktop, replace the `args` array from the setup example with `["-mode", "fair", "-save", "/absolute/path/to/My Career.fm", "-fmsave-cli", "/absolute/path/to/fmsave"]`. If the server is already registered, edit or replace the existing entry. Use just `-mode fair` to work from match statistics and eligible players you supply without a save or helper. Use `career_info` to verify the mode.

The existing setup defaults to `-mode editor` for compatibility. Fair mode restricts this MCP server. For a fair-only client session, disable any separate FM editors or unrestricted save readers registered in the client.

## Analysis tools

| Tool | Result |
| --- | --- |
| `career_info` | FM edition, snapshot dates, counts and parser warnings |
| `reload_career` | Refresh the configured inputs |
| `find_clubs` | Look up club identifiers by name |
| `squad_review` | Positional depth, loans and an optional shortlist with strict CA **and** PA cutoffs |
| `scout_players` | Filter by position, age, ability, wage and an unverified stored value |
| `build_tactic` | In-possession and out-of-possession shapes, team instructions, an XI with no duplicate players and advice on weak positions |
| `coach_brief` | Next fixtures, last-match review, saved tactic slots and a proposed XI from one save snapshot |
| `staff_audit` | Staff attributes and vacancies against the headcounts you supply |
| `review_match` | Played match analysis with the evidence, suggested changes and what to measure next |
| `search_staff_candidates` | Rank staff by the relevant coaching, recruitment or medical attributes |
| `draft_recruitment_offer` | Draft a transfer or staff contract and check it against the limits you supply |

Try “Build a high press for my club and find young replacements for the positions where the XI has weak fits” or “Show owned players with CA below 130 and PA below 145.”

Squad depth counts familiarity of at least 15 out of 20. The proposed XI uses position familiarity and CA. Check fitness, suspensions, registration and role suitability in FM. Scouting figures marked unverified have uncertain meaning. For players on loan, wage filters use the permanent contract at the parent club when available. The staff export supplies employees and roles, while your club's Staff screen supplies the allowed headcounts. Export filters and matching club names can affect the staff totals.

## More tools for match preparation

| Tool | Result |
| --- | --- |
| `fixture_plan` | Upcoming fixtures and calendar-day gaps for rotation |
| `recent_form` | Recent results, goals, xG per shot and the share of shots from distance, with report coverage |
| `compare_matches` | Before-and-after match statistics for a tactical experiment |
| `saved_lineup` | Saved starters, bench and unfilled slots |
| `training_calendar` | Saved first-team training sessions for the next 1–28 days |
| `contract_watch` | Permanent contracts expiring within a chosen window |
| `finance_history` | Monthly budget and payroll snapshots in the save's units |
| `compare_players` | Positional ratings, CA, PA and saved wages for two to five players |
| `depth_gaps` | Positions in a chosen shape without enough options for one backup |
| `draft_loan` | Proposed wage contribution, fees and estimated total against your ceiling |

The first seven tools use `-save` and `-fmsave-cli`. The last three use the player snapshot. Finance rows are historical. A loan draft uses terms you supply and cannot retrieve a club's private demands or send an offer.

## Assistant-coach workflows

**Prepare for the next match.** Ask “Who are we playing next? Keep my 4-2-3-1, rest Fred and give me one adjustment based on the last match.” With `-save` and `-fmsave-cli`, `coach_brief` reads the latest completed save on each call and accepts a tactical style and optional player exclusions. It reports the saved tactic slots and gives a separate XI proposal using the available positional ratings and CA. Use `squad_review` to resolve player UIDs. Check the active slot, fitness, suspensions and registration in FM before selecting the team.

**Review the last match.** Add `-fmsave-cli /path/to/fmsave` alongside `-save`. Install [fmsave](https://github.com/rhiever/fmsave) as a separate CLI. The server reads the managed club’s latest played first-team fixture by default. Its match exports share a temporary snapshot so an autosave cannot mix records from different versions of the career. If the save has both teams' match stats, it uses them for chance-quality analysis. On FM27 builds where team stats cannot be decoded, it uses readable player ratings and passing stats. You can also supply both teams' xG, shots and possession from the FM match screen directly to `review_match`. A score alone does not establish a tactical cause.

**Match-report compatibility.** The release includes `fmsave-fm27`, a macOS/Linux helper for the match-report layout tested on FM27 build `27.0.1+2503222`. Install `fmsave==0.12.2` under Python 3.12 or later, make that interpreter available as `python3`, then pass the helper's absolute path to `-fmsave-cli`. If you used a virtual environment, put its `bin` folder on the client's PATH or set the helper's first line to that environment's Python path. Other builds use fmsave's regular checks. The helper checks every report's framing before returning compatible rows.

**Recruit players and staff.** Use `scout_players` for players and `search_staff_candidates` for staff. For outside staff, export an unfiltered staff list from FMLibreEditor. The staff score is an average of role-relevant attributes. It does not establish a candidate's interest or wage demand. Pass an explicit fee, wage, duration and any applicable budgets to `draft_recruitment_offer` for a draft you can enter in FM. The draft does not submit a transfer bid or staff employment offer.

### Live FootballManagerMCP integration

On Windows Steam, set up the API and game plugin from [FootballManagerMCP](https://github.com/FootballManagerMCP/FootballManagerMCP). The upstream API normally listens on local port 5120. Start FM, load your career, start the upstream API and run FM Career MCP on the same PC. A port flag selects an existing API listener.

```text
-game-api-port 5120
```

This adds live match detail (goals, substitutions, lineup and player stats), current tactics, squad, scouting budget, player scout reports and the in-game database search for player and staff names. With the tactics screen opened once, `live_tactic_options` provides the game's valid keys. You can use just the live integration or combine it with a save snapshot.

Run `live_bridge_status` to verify the connection and loaded team. The bridge also exposes `live_inbox`, `live_opponent_report` and `live_role_fit` for news, opponent analysis and the game's ratings for each position and role.

Add `-game-club "Exact team name from the live API"` to enable the following:

| Tool | Action |
| --- | --- |
| `preview_live_tactic` | Validate changes to mentality, team instructions, formations, player slots, roles or bench |
| `preview_scouting_action` | Review a scouting assignment or shortlist addition |
| `preview_trial_offer` | Review an in-game player trial invitation of 1–4 weeks |
| `preview_trial_batch` | Review inviting, ending or withdrawing trials for up to 50 players. The game must confirm every requested player before the preview can be applied. |
| `apply_live_action` | Apply the confirmed preview and report failed or skipped steps |

The live bridge uses game IDs from `live_squad` or `find_live_recruits`. Saved player IDs may differ. Changing a tactic slot can make it active, and lineup changes affect the team's shared selection. Review the preview before applying. A trial invitation is separate from a transfer or employment offer. Save in FM afterward if you want the changes to persist.

The integration was tested against the upstream HTTP response shapes with a local fixture server. Actual Windows gameplay was not part of the release test. On macOS, match review and recruitment drafts work from local data. Native tactical changes can be exported with the template workflow below.

## Career loop without computer use

`career_turn` reads the next fixture, stored XI, latest match review and training plan from one stable save revision. `plan_visible_xi` can use manager-provided role ratings and exclude players marked injured, suspended, unregistered or rested. These reads work in fair mode.

Editor mode can write a training schedule into a separate save on the supported FM27 layout. Configure `-save`, `-fmsave-cli`, `-tactic-copy-cli` and `-output-dir` as for save-copy workflows. Use `preview_training_copy` and `apply_training_copy` for a named copy. Add `-auto-training balanced` to check the configured source save every 15 seconds and prepare a verified copy when a completed save revision calls for different sessions. Use `automatic_training_status` to find the output. Add `-training-worker` to run the same watcher independently of a chat client and report its status to the process log. The source career is preserved. To continue in the new copy, load it in FM and update the configured source path.

The file-based workflow requires no computer use, live Trainer app or separate MCP server. It cannot load the copy in FM, start a match, press Continue, or receive unsaved fixture changes. The included reader and writer use the pinned fmsave Python library.

## Credits and community feedback

- [FM27 Trainer by Sem Hiel](https://github.com/SemHiel2003/fm27-trainer) documented training session codes and demonstrated fixture-aware schedules.
- [Napas fm-mcp](https://github.com/Napas/fm-mcp) demonstrated role-aware squad selection. [ianderse's MatchLens MCP](https://github.com/ianderse/FMMatchLens_MCP) and [osnsyc's FMMatchLens](https://github.com/osnsyc/FMMatchLens) demonstrated evidence-based match analysis. This workflow launches none of those projects.
- [rhiever's fmsave](https://github.com/rhiever/fmsave) provides save data. The legacy Windows game connection uses [FootballManagerMCP](https://github.com/FootballManagerMCP/FootballManagerMCP) when explicitly configured.
- Training planning responds to [schedule resets](https://community.sports-interactive.com/bugtracker/previous-versions/1644_football-manager-26-bugs-tracker/all-other-issues/changes-to-the-training-schedule-resets-by-the-end-of-the-current-week-r30966/). The selection inputs address [injury concerns](https://community.sports-interactive.com/forums/topic/612913-fm27-a-lot-of-injuries/) and [reported registration gaps](https://community.sports-interactive.com/forums/topic/612416-the-official-fm27-feedback-thread/page/24/).

## Feature coverage

| Reference tool | Available here | Open gaps |
| --- | --- | --- |
| [Genie Scout](https://geniescout.com/features/) | Editor database filters, player comparison, and custom scoring and similarity from supplied scout reports | Native shortlist-file import/export, projected attributes, database-wide attribute similarity, development snapshots |
| [FMDataLab](https://www.fmdatalab.com/) | Custom visible XI, per-90 rankings, role-weighted scout reports and user-supplied role suitability | League benchmarks and role optimization for arbitrary tactics supplied to this MCP |
| [FootballManagerMCP](https://github.com/FootballManagerMCP/FootballManagerMCP) | Live match and opponent analysis, tactics, scout assignments, shortlist additions, trial batches | Recruitment focus management and a verified Mac live bridge |
| [FMRTE](https://docs.fmrte.com/) and [FMLibreEditor](https://github.com/tozmoz/FMLibreEditor) | Selected heal, holiday and tactic workflows, and staff export analysis | General player, staff, club and competition editing, reusable presets and undo |
| [Football Manager's editor](https://www.footballmanager.com/fm27/features/fm27-2d-match-engine-game-editor-and-deeper-world-upgrades) | Selected career edits through the integrations above | Pre-game database editing and full in-game editor coverage |

## Optional write tools

Write tools appear only when configured. They are intended for local single-player careers. The preview/apply flows keep a short-lived, single-use confirmation token. Review the affected players in your client before applying the change.

### Native tactic variants

Install [FM Tactics Creator](https://github.com/jamespheffernan/fm-tactics-creator/releases/tag/v0.1.2). Add these arguments:

```text
-tactics-cli /path/to/fm-tactics -tactic-template /path/to/YourTemplate.fmf -output-dir /path/to/exports
```

`export_tactic` writes a new `.fmf` from a template you exported from FM. It supports name, mentality and tackling. The template supplies formations, roles and other instructions. Use `build_tactic` for a full plan you can enter in-game, then save that plan in FM if you want a native file. Load the generated FMF from the Tactics screen.

### Width and starting XI in a save copy

On macOS or Linux, install `fmsave==0.12.2` for the Python interpreter used by [tools/fm-tactic-copy](tools/fm-tactic-copy). Configure it with:

```text
-tactic-copy-cli /absolute/path/to/fm-tactic-copy -output-dir /path/to/exports
```

Use `saved_lineup` and `scout_players` to find player UIDs. `preview_tactic_copy` can set attacking width in the first saved tactic and replace the shared first-team XI. Supply all 11 UIDs in that tactic's stored slot order for an XI change. `apply_tactic_copy` verifies the new file and requires an unchanged source revision and an unused output name. The adapter has been checked on FM27 build `27.0.1+2503222`. Load the resulting file in FM and check the active tactic and player eligibility. This workflow edits a separate file on disk and has no live connection to FM.

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

For comparisons, [Napas/fm-mcp](https://github.com/Napas/fm-mcp) reads richer squad and match details from saved careers, [FM27 Trainer](https://github.com/SemHiel2003/fm27-trainer) connects to the game to work with the training calendar, and [FMMatchLens MCP](https://github.com/ianderse/FMMatchLens_MCP) analyses recordings from its game plugin. Each uses a different source. Further live work includes macOS save and tactic commands, training changes, normal transfer and contract offers, and match event analysis. Each needs a verified game-side operation or source on the target platform.

## Development

```sh
go test ./...
go vet ./...
go build ./...
```

The test suite builds its own data. Keep real careers, player exports, FMF templates and client credentials out of the repository.

GPL-3.0-or-later. Independent community project. Football Manager is a trademark of Sports Interactive. Built with the [official MCP Go SDK](https://go.sdk.modelcontextprotocol.io/), [fm-open-scout](https://github.com/YanivGabay/fm-open-scout) for save parsing, [FMLibreEditor](https://github.com/tozmoz/FMLibreEditor) for live injuries and [FM Tactics Creator](https://github.com/jamespheffernan/fm-tactics-creator) for native tactic variants. Archive format reference: [fm-open-scout FORMAT.md](https://github.com/YanivGabay/fm-open-scout/blob/main/FORMAT.md).
