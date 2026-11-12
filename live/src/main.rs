// SPDX-License-Identifier: GPL-3.0-or-later

#[cfg(not(any(target_os = "macos", windows)))]
fn main() {
    eprintln!("The FM27 live adapter supports macOS and Windows.");
    std::process::exit(1);
}

#[cfg(any(target_os = "macos", windows))]
mod live {
    use anyhow::{Context, Result, ensure};
    use fmlibre_core::{ArraySource, GameInfo, PlayerIndex, Session, find_game};
    use fmlibre_mem::Process;
    use serde_json::{Value, json};
    use std::{collections::BTreeSet, io::Read};

    pub fn run() -> Result<()> {
        let args: Vec<_> = std::env::args().skip(1).collect();
        ensure!(
            (3..=4).contains(&args.len()) && (args[0] == "preview" || args[0] == "apply"),
            "usage: fm-career-live preview|apply PID CLUB_UID [--allow-unknown-build]"
        );
        let allow_unknown = args.get(3).is_some_and(|arg| arg == "--allow-unknown-build");
        ensure!(args.len() == 3 || allow_unknown, "unknown option");
        let apply = args[0] == "apply";
        let pid = find_game(Some(args[1].parse()?))?;
        let club_uid: u32 = args[2].parse()?;
        let process = if apply {
            Process::open_read_write(pid)?
        } else {
            Process::open_read(pid)?
        };
        ensure!(GameInfo::inspect(&process)?.edition == Some(27), "expected FM27");
        let session = Session::connect(process, ArraySource::ProfileFirst)?;
        if allow_unknown {
            session.allow_unknown_build();
        }
        ensure!(session.structure_lock().is_none(), "live checks failed: {:?}", session.structure_lock());
        let loads = session.loads().context("cannot identify the loaded FM career")?;
        let date = session.game_date().context("cannot read in-game date")?;
        ensure!(
            session.staff()?.iter().any(|p| p.is_human && p.club_uid == Some(club_uid)),
            "the configured club is not managed by a human in this career"
        );
        let club = session.clubs()?.into_iter().find(|c| c.uid == club_uid).context("club missing")?;
        let players = PlayerIndex::new(session.players()?);
        let owned: BTreeSet<_> = session.club_teams(&club, &players)?
            .into_iter()
            .flat_map(|team| team.players)
            .filter(|p| p.plays_here && p.loan.is_none() && p.injuries > 0)
            .map(|p| p.uid)
            .collect();
        let mut targets = Vec::new();
        let mut injuries = Vec::new();
        let mut plans = Vec::new();
        for uid in owned {
            let player = players.get(uid).context("player disappeared from the club")?;
            let current = session.injuries(player)?;
            if !current.is_empty() {
                targets.push(json!({ "uid": uid, "name": player.name, "injuries": current.len() }));
                injuries.push(json!({ "uid": uid, "records": current }));
                plans.push(session.plan_heal(player)?);
            }
        }
        let result = json!({
            "club": club.name,
            "targets": targets,
            "state": { "pid": pid, "club": club_uid, "loads": loads, "date": date,
                "build": session.fingerprint.to_string(), "injuries": injuries }
        });

        if apply {
            let mut input = Vec::new();
            std::io::stdin().take(1_048_577).read_to_end(&mut input)?;
            ensure!(input.len() <= 1_048_576, "preview is too large");
            let expected: Value = serde_json::from_slice(&input)?;
            ensure!(expected == result && !plans.is_empty(), "career or injuries changed; preview again");
            let mut applied = Vec::new();
            let update = (|| -> Result<()> {
                for plan in &plans {
                    ensure!(!session.save_changed() && session.game_date() == Some(date), "career changed during healing");
                    applied.push(session.apply_heal(plan)?);
                    let player = players.get(plan.player).context("player disappeared")?;
                    ensure!(session.injuries(player)?.is_empty(), "healing verification failed");
                }
                Ok(())
            })();
            if let Err(err) = update {
                for done in applied.iter().rev() {
                    if let Err(rollback) = session.undo_heal(done) {
                        eprintln!("Undo could not restore a player: {rollback}");
                    }
                }
                return Err(err);
            }
        }
        println!("{result}");
        Ok(())
    }
}

#[cfg(any(target_os = "macos", windows))]
fn main() {
    let result = std::thread::Builder::new()
        .stack_size(16 * 1024 * 1024)
        .spawn(live::run)
        .expect("start live adapter")
        .join()
        .expect("live adapter panicked");
    if let Err(err) = result {
        eprintln!("{err:#}");
        std::process::exit(1);
    }
}
