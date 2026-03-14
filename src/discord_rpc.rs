use std::{
    env,
    thread,
    time::{SystemTime, UNIX_EPOCH},
};

use crossbeam_channel::{unbounded, Receiver, Sender};
use discord_rich_presence::{
    activity::{Activity, ActivityType, Assets, Button, StatusDisplayType, Timestamps},
    DiscordIpc, DiscordIpcClient,
};

const DEFAULT_DISCORD_APPLICATION_ID: &str = "1482325444308238357";

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiscordPresence {
    pub title: String,
    pub artist: String,
    pub track_id: String,
    pub album_art_url: Option<String>,
    pub current_ms: u64,
    pub total_ms: u64,
    pub is_playing: bool,
}

enum DiscordCommand {
    Update(DiscordPresence),
    Clear,
    Shutdown,
}

pub struct DiscordRpcHandle {
    tx: Sender<DiscordCommand>,
    worker: Option<thread::JoinHandle<()>>,
}

impl DiscordRpcHandle {
    pub fn new() -> Self {
        let (tx, rx) = unbounded();
        let worker = thread::spawn(move || discord_worker(rx));
        Self {
            tx,
            worker: Some(worker),
        }
    }

    pub fn update(&self, presence: DiscordPresence) {
        let _ = self.tx.send(DiscordCommand::Update(presence));
    }

    pub fn clear(&self) {
        let _ = self.tx.send(DiscordCommand::Clear);
    }

    pub fn shutdown(mut self) {
        let _ = self.tx.send(DiscordCommand::Shutdown);
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

fn discord_worker(rx: Receiver<DiscordCommand>) {
    let application_id = env::var("DISCORD_RPC_CLIENT_ID")
        .ok()
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| DEFAULT_DISCORD_APPLICATION_ID.to_owned());

    let mut client: Option<DiscordIpcClient> = None;

    while let Ok(command) = rx.recv() {
        match command {
            DiscordCommand::Update(presence) => {
                if ensure_connected(&mut client, &application_id) {
                    let activity = build_activity(&presence);
                    if let Some(client) = client.as_mut() {
                        if client.set_activity(activity.clone()).is_err() {
                            if client.reconnect().is_ok() {
                                let _ = client.set_activity(activity);
                            }
                        }
                    }
                }
            }
            DiscordCommand::Clear => {
                clear_presence(&mut client, &application_id);
            }
            DiscordCommand::Shutdown => {
                clear_presence(&mut client, &application_id);
                // Give Discord a brief moment to apply the clear payload.
                std::thread::sleep(std::time::Duration::from_millis(120));
                if let Some(client) = client.as_mut() {
                    let _ = client.close();
                }
                break;
            }
        }
    }

    clear_presence(&mut client, &application_id);
    if let Some(client) = client.as_mut() {
        let _ = client.close();
    }
}

fn clear_presence(client: &mut Option<DiscordIpcClient>, application_id: &str) {
    if client.is_none() {
        let mut next = DiscordIpcClient::new(application_id);
        if next.connect().is_ok() {
            *client = Some(next);
        }
    }

    if let Some(client) = client.as_mut() {
        if client.clear_activity().is_err() {
            if client.reconnect().is_ok() {
                let _ = client.clear_activity();
            }
        }
    }
}

fn ensure_connected(client: &mut Option<DiscordIpcClient>, application_id: &str) -> bool {
    if client.is_some() {
        return true;
    }

    let mut next = DiscordIpcClient::new(application_id);
    if next.connect().is_ok() {
        *client = Some(next);
        true
    } else {
        false
    }
}

fn build_activity(presence: &DiscordPresence) -> Activity<'static> {
    let deezer_track_url = format!("https://www.deezer.com/track/{}", presence.track_id);

    let mut activity = Activity::new()
        .activity_type(ActivityType::Listening)
        .status_display_type(StatusDisplayType::Name)
        .name(presence.artist.clone())
        .details(if presence.is_playing {
            presence.title.clone()
        } else {
            format!("{} (Paused)", presence.title)
        })
        .state(presence.artist.clone())
        .buttons(vec![Button::new("Open in Deezer", deezer_track_url.clone())]);

    if let Some(album_art_url) = presence.album_art_url.as_ref() {
        activity = activity.assets(
            Assets::new()
                .large_image(album_art_url.clone())
                .large_text(format!("{} - {}", presence.title, presence.artist))
                .large_url(deezer_track_url),
        );
    }

    if presence.is_playing && presence.total_ms > presence.current_ms {
        let now_ms = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_millis() as i64)
            .unwrap_or(0);
        let start_ms = now_ms.saturating_sub(presence.current_ms as i64);
        let end_ms = start_ms.saturating_add(presence.total_ms as i64);
        activity = activity.timestamps(Timestamps::new().start(start_ms).end(end_ms));
    }

    activity
}